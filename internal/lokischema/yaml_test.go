package lokischema

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-030

import "gopkg.in/yaml.v3"

func yamlUnmarshal(data []byte, target any) error {
	return yaml.Unmarshal(data, target)
}
