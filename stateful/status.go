package stateful

import "context"

type ReferenceUsage struct {
	Rows       *int64 `json:"rows"`
	TableBytes *int64 `json:"tableBytes"`
}
type ReferenceStatus struct {
	Storage       FeatureStatus   `json:"storage"`
	HistoryStatus FeatureStatus   `json:"historyStatus"`
	Artifacts     FeatureStatus   `json:"artifacts"`
	Credentials   FeatureStatus   `json:"credentials"`
	History       *ReferenceUsage `json:"history,omitempty"`
}

func configurationFeature(component, message, action string) FeatureStatus {
	return FeatureStatus{Mode: "enabled", Status: "configuration_error", Diagnostic: &Diagnostic{Code: "reference_configuration_error", Component: component, Operation: "configure", RequestID: "", InferenceOutcome: "independent", PersistenceOutcome: "unavailable", Cause: "configuration", Message: message, Action: action}}
}

func (s *Service) Status(ctx context.Context, hash string) (ReferenceStatus, error) {
	if err := checkScope(hash); err != nil {
		return ReferenceStatus{}, err
	}
	status := ReferenceStatus{Storage: FeatureStatus{Mode: "enabled", Status: "configured"}, HistoryStatus: FeatureStatus{Mode: "disabled", Status: "disabled"}, Artifacts: s.artifactFeature(), Credentials: FeatureStatus{Mode: "enabled", Status: "configured"}}
	if s.config.Pool == nil {
		status.Storage = configurationFeature("storage", "Reference PostgreSQL is not configured.", "Configure HARDEN_LLM_DATABASE_URL; cache-off inference remains available.")
	}
	if s.config.Vault == nil {
		status.Credentials = configurationFeature("credentials", "Entered provider credentials cannot be encrypted.", "Configure the deployment's HARDEN_LLM_CREDENTIAL_KEY.")
	}
	if s.config.ArtifactConfigurationError != nil {
		status.Artifacts = FeatureStatus{Mode: "enabled", Status: "configuration_error", Diagnostic: s.config.ArtifactConfigurationError}
	}
	if !s.config.History.Enabled {
		return status, nil
	}
	status.HistoryStatus = FeatureStatus{Mode: "enabled", Status: "configured"}
	status.History = &ReferenceUsage{}
	if s.config.Pool == nil {
		status.HistoryStatus = status.Storage
		return status, nil
	}
	usage, err := s.historyUsage(ctx, hash)
	if err != nil {
		status.HistoryStatus = FeatureStatus{Mode: "enabled", Status: "unavailable", Diagnostic: &Diagnostic{Code: "history_read_failed", Component: "history", Operation: "usage", RequestID: "", InferenceOutcome: "independent", PersistenceOutcome: "unavailable", Cause: safeCause(err), Message: "History size figures are unavailable.", Action: "Check PostgreSQL connectivity, schema version and role permissions."}}
		status.Storage = status.HistoryStatus
		return status, nil
	}
	status.History.Rows = &usage.Rows
	status.History.TableBytes = &usage.TableBytes
	return status, nil
}
