// Tests of the Windows known folders, against the environment variables
// Windows sets from the same records.

package host

import (
	"os"
	"strings"
	"testing"
)

func TestKnownFolders(t *testing.T) {
	for folder, variable := range map[Folder]string{
		Profile:      "USERPROFILE",
		Public:       "PUBLIC",
		Windows:      "SystemRoot",
		ProgramFiles: "ProgramFiles",
		ProgramData:  "ProgramData",
	} {
		want := os.Getenv(variable)
		if want == "" {
			t.Logf("%%%s%% is not set", variable)
			continue
		}
		got, err := folder.Path()
		if err != nil || !strings.EqualFold(got, want) {
			t.Errorf("%s = %q, %v; %%%s%% is %q", folder, got, err, variable, want)
		}
	}
	for _, folder := range []Folder{Documents, UserProfiles, ProgramFilesX86} {
		if path, err := folder.Path(); err != nil || path == "" {
			t.Errorf("%s = %q, %v", folder, path, err)
		}
	}
}
