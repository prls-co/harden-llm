package runtime

type ObservabilityContext struct {
	TaskID         string            `json:"taskId,omitempty"`
	TaskSlug       string            `json:"taskSlug,omitempty"`
	ItemID         string            `json:"itemId,omitempty"`
	RunID          string            `json:"runId,omitempty"`
	OrganizationID string            `json:"organizationId,omitempty"`
	QuerySetID     string            `json:"querySetId,omitempty"`
	Environment    string            `json:"environment,omitempty"`
	Release        string            `json:"release,omitempty"`
	PromptLabels   []string          `json:"promptLabels,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

func MergeObservabilityContext(defaults, request ObservabilityContext) ObservabilityContext {
	result := defaults.clone()
	mergeScalar(&result.TaskID, request.TaskID)
	mergeScalar(&result.TaskSlug, request.TaskSlug)
	mergeScalar(&result.ItemID, request.ItemID)
	mergeScalar(&result.RunID, request.RunID)
	mergeScalar(&result.OrganizationID, request.OrganizationID)
	mergeScalar(&result.QuerySetID, request.QuerySetID)
	mergeScalar(&result.Environment, request.Environment)
	mergeScalar(&result.Release, request.Release)
	if request.PromptLabels != nil {
		result.PromptLabels = append([]string(nil), request.PromptLabels...)
	}
	result.Tags = mergeStringMap(result.Tags, request.Tags)
	result.Metadata = mergeStringMap(result.Metadata, request.Metadata)
	return result
}

func MetricLabels(context ObservabilityContext) map[string]string {
	labels := make(map[string]string, 1)
	if context.Environment != "" {
		labels["environment"] = context.Environment
	}
	return labels
}

func (context ObservabilityContext) clone() ObservabilityContext {
	context.PromptLabels = append([]string(nil), context.PromptLabels...)
	context.Tags = mergeStringMap(nil, context.Tags)
	context.Metadata = mergeStringMap(nil, context.Metadata)
	return context
}

func mergeScalar(target *string, value string) {
	if value != "" {
		*target = value
	}
}

func mergeStringMap(base, override map[string]string) map[string]string {
	if base == nil && override == nil {
		return nil
	}
	result := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range override {
		result[key] = value
	}
	return result
}
