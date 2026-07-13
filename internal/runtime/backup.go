package runtime

import (
	"fmt"
	"strings"

	"github.com/prls-co/harden-llm/internal/retry"
)

const maxBackupDepth = 5

type ProfileNode struct {
	ID      string
	Backups []string
}

func BuildBackupPlan(primary string, profiles map[string]ProfileNode) ([]string, error) {
	if _, ok := profiles[primary]; !ok {
		return nil, fmt.Errorf("primary profile %q was not found", primary)
	}
	for key, profile := range profiles {
		if profile.ID == "" || profile.ID != key {
			return nil, fmt.Errorf("profile key %q must match profile ID %q", key, profile.ID)
		}
		seen := make(map[string]struct{}, len(profile.Backups))
		for _, backup := range profile.Backups {
			if backup == profile.ID {
				return nil, fmt.Errorf("profile %q cannot reference itself as a backup profile", profile.ID)
			}
			if _, duplicate := seen[backup]; duplicate {
				return nil, fmt.Errorf("duplicate backup profile %q on %q", backup, profile.ID)
			}
			seen[backup] = struct{}{}
			if _, exists := profiles[backup]; !exists {
				return nil, fmt.Errorf("backup profile %q was not found", backup)
			}
		}
	}

	for _, root := range profiles {
		if err := validateDepthAndCycles(root.ID, root.ID, profiles, 0, nil); err != nil {
			return nil, err
		}
	}

	plan := make([]string, 0, len(profiles))
	planned := make(map[string]struct{}, len(profiles))
	var appendPlan func(string)
	appendPlan = func(id string) {
		if _, exists := planned[id]; exists {
			return
		}
		planned[id] = struct{}{}
		plan = append(plan, id)
		for _, backup := range profiles[id].Backups {
			appendPlan(backup)
		}
	}
	appendPlan(primary)
	return plan, nil
}

func validateDepthAndCycles(root, current string, profiles map[string]ProfileNode, depth int, stack []string) error {
	if depth > maxBackupDepth {
		return fmt.Errorf("backup profile depth cannot exceed %d for %q", maxBackupDepth, root)
	}
	for _, item := range stack {
		if item == current {
			return fmt.Errorf("backup profile cycle includes %q (%s)", current, strings.Join(append(stack, current), " -> "))
		}
	}
	nextStack := append(append([]string(nil), stack...), current)
	for _, backup := range profiles[current].Backups {
		if err := validateDepthAndCycles(root, backup, profiles, depth+1, nextStack); err != nil {
			return err
		}
	}
	return nil
}

func BackupEligible(classification retry.Classification) bool {
	switch classification.Category {
	case retry.CategoryNetwork, retry.CategoryRateLimit, retry.CategoryServer:
		return true
	default:
		return false
	}
}
