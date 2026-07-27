//go:build windows

package processmetrics

import (
	"errors"
	"math"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// processAccess 只授予读取基础信息与 working set 所需权限。
	processAccess = windows.PROCESS_QUERY_INFORMATION | windows.PROCESS_VM_READ
)

var (
	// psapi 只解析 Windows inbox API，不加载第三方 native code。
	psapi = windows.NewLazySystemDLL("psapi.dll")
	// getProcessMemoryInfo 读取 documented PROCESS_MEMORY_COUNTERS_EX。
	getProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
	// kernel32 只解析 Windows inbox handle counter API。
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	// getProcessHandleCount 读取目标进程当前 handle 数。
	getProcessHandleCount = kernel32.NewProc("GetProcessHandleCount")
)

// processMemoryCountersEX 是 Win32 PROCESS_MEMORY_COUNTERS_EX 的 ABI layout。
type processMemoryCountersEX struct {
	// Size 是调用方提供的结构宽度。
	Size uint32
	// PageFaultCount 是累计 page fault；当前 evidence 不输出。
	PageFaultCount uint32
	// PeakWorkingSetSize 是峰值 working set；当前 report 使用显式窗口聚合。
	PeakWorkingSetSize uintptr
	// WorkingSetSize 是当前驻留 working set。
	WorkingSetSize uintptr
	// QuotaPeakPagedPoolUsage 是 Win32 ABI 保留字段。
	QuotaPeakPagedPoolUsage uintptr
	// QuotaPagedPoolUsage 是 Win32 ABI 保留字段。
	QuotaPagedPoolUsage uintptr
	// QuotaPeakNonPagedPoolUsage 是 Win32 ABI 保留字段。
	QuotaPeakNonPagedPoolUsage uintptr
	// QuotaNonPagedPoolUsage 是 Win32 ABI 保留字段。
	QuotaNonPagedPoolUsage uintptr
	// PagefileUsage 是 Win32 ABI 保留字段。
	PagefileUsage uintptr
	// PeakPagefileUsage 是 Win32 ABI 保留字段。
	PeakPagefileUsage uintptr
	// PrivateUsage 是 Win32 ABI 保留字段。
	PrivateUsage uintptr
}

// Sampler 持有 exact process handle 与不可变 creation time。
type Sampler struct {
	// mutex 串行化 sequence 与 Close。
	mutex sync.Mutex
	// handle 是只读 process handle，不进入 Sample。
	handle windows.Handle
	// processID 只用于按 owner process 统计 thread，不进入 Sample。
	processID uint32
	// creationTime 防止 PID reuse 或错误 handle 替换。
	creationTime windows.Filetime
	// started 为 Sample 提供 Go monotonic 时间基线。
	started time.Time
	// sequence 是最后成功样本序列。
	sequence uint64
	// closed 禁止 cleanup 后继续采样。
	closed bool
}

// New 打开 exact PID 的只读 Windows process sampler。
func New(processID int) (*Sampler, error) {
	if processID <= 0 || uint64(processID) > math.MaxUint32 {
		return nil, errors.New("process sampler identity is invalid")
	}
	handle, err := windows.OpenProcess(processAccess, false, uint32(processID))
	if err != nil {
		return nil, errors.New("open process sampler target")
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil ||
		exit != (windows.Filetime{}) {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("read process sampler identity")
	}
	return &Sampler{
		handle: handle, processID: uint32(processID),
		creationTime: creation, started: time.Now(),
	}, nil
}

// Sample 读取累计 CPU、working set、handle 与 thread，不读取进程内存内容。
func (sampler *Sampler) Sample() (Sample, error) {
	if sampler == nil {
		return Sample{}, errors.New("process sampler is nil")
	}
	sampler.mutex.Lock()
	defer sampler.mutex.Unlock()
	if sampler.closed || sampler.handle == 0 ||
		sampler.sequence == math.MaxUint64 {
		return Sample{}, errors.New("process sampler is closed or exhausted")
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(
		sampler.handle, &creation, &exit, &kernel, &user,
	); err != nil || creation != sampler.creationTime ||
		exit != (windows.Filetime{}) {
		return Sample{}, errors.New("process sampler target is stale")
	}
	memory, err := sampleMemory(sampler.handle)
	if err != nil {
		return Sample{}, err
	}
	handles, err := sampleHandles(sampler.handle)
	if err != nil {
		return Sample{}, err
	}
	threads, err := sampleThreads(sampler.processID)
	if err != nil {
		return Sample{}, err
	}
	elapsed := time.Since(sampler.started).Microseconds()
	if elapsed < 0 {
		return Sample{}, errors.New("process sampler monotonic clock regressed")
	}
	kernelNanoseconds := filetimeDurationNS(kernel)
	userNanoseconds := filetimeDurationNS(user)
	if math.MaxUint64-kernelNanoseconds < userNanoseconds {
		return Sample{}, errors.New("process sampler CPU counter overflowed")
	}
	cpuNanoseconds := kernelNanoseconds + userNanoseconds
	sampler.sequence++
	return Sample{
		Sequence: sampler.sequence, MonotonicTimeUS: uint64(elapsed),
		CPUTimeNS:       cpuNanoseconds,
		WorkingSetBytes: uint64(memory.WorkingSetSize),
		HandleCount:     handles, ThreadCount: threads,
	}, nil
}

// filetimeDurationNS 把 process time 的 100 ns interval 转为无 epoch duration。
func filetimeDurationNS(value windows.Filetime) uint64 {
	intervals := uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
	return intervals * 100
}

// Close 释放 process handle；重复调用安全。
func (sampler *Sampler) Close() error {
	if sampler == nil {
		return nil
	}
	sampler.mutex.Lock()
	if sampler.closed {
		sampler.mutex.Unlock()
		return nil
	}
	sampler.closed = true
	handle := sampler.handle
	sampler.handle = 0
	sampler.mutex.Unlock()
	if handle == 0 {
		return nil
	}
	if err := windows.CloseHandle(handle); err != nil {
		return errors.New("close process sampler handle")
	}
	return nil
}

// sampleMemory 调用 Win32 GetProcessMemoryInfo 的 fixed EX layout。
func sampleMemory(handle windows.Handle) (processMemoryCountersEX, error) {
	var counters processMemoryCountersEX
	counters.Size = uint32(unsafe.Sizeof(counters))
	result, _, _ := getProcessMemoryInfo.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.Size),
	)
	if result == 0 {
		return processMemoryCountersEX{}, errors.New("read process working set")
	}
	return counters, nil
}

// sampleHandles 调用 Win32 GetProcessHandleCount。
func sampleHandles(handle windows.Handle) (uint32, error) {
	var count uint32
	result, _, _ := getProcessHandleCount.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&count)),
	)
	if result == 0 {
		return 0, errors.New("read process handle count")
	}
	return count, nil
}

// sampleThreads 从系统 snapshot 只计数 exact owner PID。
func sampleThreads(processID uint32) (uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(
		windows.TH32CS_SNAPPROCESS, 0,
	)
	if err != nil {
		return 0, errors.New("create process snapshot")
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return 0, errors.New("read first process snapshot entry")
	}
	for {
		if entry.ProcessID == processID {
			return entry.Threads, nil
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return 0, errors.New("process disappeared during thread sample")
			}
			return 0, errors.New("read process snapshot entry")
		}
	}
}
