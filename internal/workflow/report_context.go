package workflow

import "context"

// ReportContext restores recorded storage and execution requirements for
// inspection without loading provider credentials or starting inference.
func ReportContext(ctx context.Context, r Report) (context.Context, func(), error) {
	prepared, err := PrepareProcessContainment(ctx, r.ProcessContainment)
	if err != nil {
		return ctx, func() {}, err
	}
	prepared, cleanup, err := PrepareReportIsolation(prepared, r)
	if err != nil {
		return ctx, func() {}, err
	}
	prepared, err = ReportSnapshotContext(prepared, r)
	if err != nil {
		cleanup()
		return ctx, func() {}, err
	}
	return prepared, cleanup, nil
}
