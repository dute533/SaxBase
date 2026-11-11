package objects

import "context"

// Inspect reports the recorded release position without changing database state.
func (s *store) Inspect(ctx context.Context) (Inspection, error) {
	var result Inspection
	var err error
	result.Current, err = s.currentVersion(ctx, s.db)
	if err != nil {
		return result, err
	}
	result.History, err = s.History(ctx)
	return result, err
}
