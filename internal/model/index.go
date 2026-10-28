package model

import "reflect"

// EqualIndexMetadata ignores excerpts and normalizes empty optional collections:
// encoding/json omits both nil and empty values from the index representation.
func EqualIndexMetadata(a, b MemoryUnit) bool {
	normalize := func(unit *MemoryUnit) {
		unit.Content = ""
		unit.OccurredAt = unit.OccurredAt.UTC().Round(0)
		if len(unit.RequirementIDs) == 0 {
			unit.RequirementIDs = nil
		}
		if len(unit.Repos) == 0 {
			unit.Repos = nil
		}
		if len(unit.Entities) == 0 {
			unit.Entities = nil
		}
		if len(unit.Metadata) == 0 {
			unit.Metadata = nil
		}
	}
	normalize(&a)
	normalize(&b)
	return reflect.DeepEqual(a, b)
}
