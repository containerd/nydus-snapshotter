package daemon

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildDeduplicationCommand(t *testing.T) {
	bootstrapPath := "/path/to/bootstrap"
	configPath := "/path/to/config.json"
	nydusImagePath := "/path/to/nydus-image"

	cmd := buildDeduplicationCommand(bootstrapPath, configPath, nydusImagePath)

	expectedArgs := []string{
		"dedup",
		"--bootstrap", bootstrapPath,
		"--config", configPath,
	}
	expectedCmd := fmt.Sprintf("%s %s", nydusImagePath, strings.Join(expectedArgs, " "))

	if expectedCmd != cmd.String() {
		t.Errorf("unexpected command string '%s'", cmd.String())
	}
}
