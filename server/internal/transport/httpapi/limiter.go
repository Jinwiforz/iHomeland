package httpapi

import (
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
)

// rateBucket 是单个operation/subject的有界token bucket运行态，不是业务事实。
type rateBucket struct {
	// tokens 是当前可用预算，上限为policy burst。
	tokens float64
	// updatedAt 是上次补充token的时间。
	updatedAt time.Time
	// lastSeen 用于idle回收与容量驱逐。
	lastSeen time.Time
}

// limiter 保存容量有硬上限且无后台timer的进程内安全下限。
//
// buckets由mutex保护，policies与容量配置在构造后只读；allow可被并发请求调用，持锁期间
// 只执行内存计算，不调用logger、observer或业务依赖。状态不是分布式配额或业务事实。
type limiter struct {
	// mutex 保护全部bucket。
	mutex sync.Mutex
	// buckets 以operation、阶段和受信subject组成，不进入日志或metrics。
	buckets map[string]rateBucket
	// policies 是启动时验证的operation预算副本。
	policies map[string]config.RatePolicy
	// maximumEntries 限制攻击者制造的IP/session基数。
	maximumEntries int
	// idleTTL 控制惰性回收时间。
	idleTTL time.Duration
	// sweepInterval 限制全表惰性回收的执行频率。
	sweepInterval time.Duration
	// nextSweepAt 是下一次允许扫描idle bucket的时间。
	nextSweepAt time.Time
}

// newLimiter 复制启动配置，不创建goroutine或外部状态。
func newLimiter(policies map[string]config.RatePolicy, maximumEntries int, idleTTL time.Duration) *limiter {
	copyPolicies := make(map[string]config.RatePolicy, len(policies))
	for operation, policy := range policies {
		copyPolicies[operation] = policy
	}
	sweepInterval := idleTTL
	if sweepInterval > time.Minute {
		sweepInterval = time.Minute
	}
	return &limiter{buckets: make(map[string]rateBucket), policies: copyPolicies, maximumEntries: maximumEntries, idleTTL: idleTTL, sweepInterval: sweepInterval}
}

// allow 原子消费一个token并返回是否允许及有界重试时间。
//
// 新subject在容量已满时fail closed；idle bucket仅在请求路径惰性回收。时钟未前进时不会
// 补充token，避免回拨意外放宽预算。
func (limiter *limiter) allow(operation string, stage string, subject string, now time.Time) (bool, time.Duration) {
	if limiter == nil || subject == "" || now.IsZero() {
		return false, time.Second
	}
	policy, exists := limiter.policies[operation]
	if !exists || policy.Requests <= 0 || policy.Burst <= 0 || policy.Window <= 0 {
		return false, time.Second
	}
	key := operation + "\x00" + stage + "\x00" + subject
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	if limiter.nextSweepAt.IsZero() {
		limiter.nextSweepAt = now.Add(limiter.sweepInterval)
	} else if !now.Before(limiter.nextSweepAt) {
		for candidate, bucket := range limiter.buckets {
			if now.Sub(bucket.lastSeen) >= limiter.idleTTL {
				delete(limiter.buckets, candidate)
			}
		}
		limiter.nextSweepAt = now.Add(limiter.sweepInterval)
	}
	bucket, found := limiter.buckets[key]
	if !found {
		if len(limiter.buckets) >= limiter.maximumEntries {
			return false, time.Second
		}
		bucket = rateBucket{tokens: float64(policy.Burst), updatedAt: now, lastSeen: now}
	}
	elapsed := now.Sub(bucket.updatedAt)
	if elapsed > 0 {
		bucket.tokens += elapsed.Seconds() * float64(policy.Requests) / policy.Window.Seconds()
		if bucket.tokens > float64(policy.Burst) {
			bucket.tokens = float64(policy.Burst)
		}
		bucket.updatedAt = now
	}
	bucket.lastSeen = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		limiter.buckets[key] = bucket
		return true, 0
	}
	limiter.buckets[key] = bucket
	retry := time.Duration((1 - bucket.tokens) / float64(policy.Requests) * policy.Window.Seconds() * float64(time.Second))
	if retry < time.Second {
		retry = time.Second
	}
	if retry > policy.Window {
		retry = policy.Window
	}
	return false, retry
}
