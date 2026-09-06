package objects

import "context"

// Inspect reports unversioned state and pending rollback without treating them
// as query failures, so the planner can explain them alongside other changes.
func (s *store) Inspect(ctx context.Context) (Inspection, error) {
	var result Inspection
	var err error
	result.Current, err = currentVersion(ctx, s.db)
	if err != nil {
		return result, err
	}
	result.History, err = s.History(ctx)
	if err != nil {
		return result, err
	}
	result.Rollbacks, err = s.Rollbacks(ctx)
	return result, err
}
