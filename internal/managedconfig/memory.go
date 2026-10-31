package managedconfig

import "fmt"

// MemoryPolicy controls logical retention and the live value/index allocation.
// Expiry denies reads immediately; owner pruning removes expired payloads.
type MemoryPolicy struct {
	DefaultTTLSeconds int   `yaml:"default_ttl_seconds" json:"default_ttl_seconds"`
	MaxTTLSeconds     int   `yaml:"max_ttl_seconds" json:"max_ttl_seconds"`
	MaxTotalBytes     int64 `yaml:"max_total_bytes" json:"max_total_bytes"`
}

func (p MemoryPolicy) Normalized() MemoryPolicy {
	if p.MaxTTLSeconds == 0 {
		p.MaxTTLSeconds = 31536000
	}
	if p.MaxTotalBytes == 0 {
		p.MaxTotalBytes = 64 << 20
	}
	return p
}
func (p MemoryPolicy) Validate() error {
	p = p.Normalized()
	if p.MaxTTLSeconds < 1 || p.MaxTTLSeconds > 315360000 || p.DefaultTTLSeconds < 0 || p.DefaultTTLSeconds > p.MaxTTLSeconds {
		return fmt.Errorf("memory_policy requires 0 <= default_ttl_seconds <= max_ttl_seconds <= 315360000")
	}
	if p.MaxTotalBytes < 1024 || p.MaxTotalBytes > 1<<40 {
		return fmt.Errorf("memory_policy.max_total_bytes must be between 1024 and 1099511627776")
	}
	return nil
}
