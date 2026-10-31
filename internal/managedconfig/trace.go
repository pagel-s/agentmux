package managedconfig

import "fmt"

type Trace struct {
	Enabled       bool  `yaml:"enabled" json:"enabled"`
	MaxTurnBytes  int64 `yaml:"max_turn_bytes" json:"max_turn_bytes"`
	MaxTotalBytes int64 `yaml:"max_total_bytes" json:"max_total_bytes"`
}

func (t Trace) Normalized() Trace {
	if t.MaxTurnBytes == 0 {
		t.MaxTurnBytes = 32 << 20
	}
	if t.MaxTotalBytes == 0 {
		t.MaxTotalBytes = 256 << 20
	}
	return t
}
func (t Trace) Validate() error {
	t = t.Normalized()
	if t.MaxTurnBytes < 4096 || t.MaxTurnBytes > 1<<30 || t.MaxTotalBytes < t.MaxTurnBytes || t.MaxTotalBytes > 1<<40 {
		return fmt.Errorf("trace requires 4096 <= max_turn_bytes <= 1GiB and max_turn_bytes <= max_total_bytes <= 1TiB")
	}
	return nil
}
