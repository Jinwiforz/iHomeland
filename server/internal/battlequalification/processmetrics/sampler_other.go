//go:build !windows

package processmetrics

// Sampler 是非 Windows build 的显式 unsupported owner。
type Sampler struct{}

// New 在未资格平台稳定拒绝 process sampler。
func New(_ int) (*Sampler, error) { return nil, ErrUnsupported }

// Sample 在未资格平台稳定拒绝 process sampler。
func (*Sampler) Sample() (Sample, error) { return Sample{}, ErrUnsupported }

// Close 对未创建资源保持幂等。
func (*Sampler) Close() error { return nil }
