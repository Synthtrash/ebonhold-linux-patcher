package ui

import (
	"errors"
	"os/exec"
	"testing"
)

func TestClassifyLaunchIssue(t *testing.T) {
	cases := []struct {
		name, manager, output string
		err                  error
		want                 launchIssue
	}{
		{name: "successful non-diagnostic launch", want: ""},
		{name: "DLL names in a successful launch", output: "Loaded ClientExt.dll and vcruntime140.dll", want: ""},
		{name: "Wine display despite zero exit", manager: "bottles-native", output: `err:winediag:nodrv_CreateWindow "Make sure that your X server is running and that $DISPLAY is set correctly."`, want: issueWineDisplay},
		{name: "missing VC runtime", output: "err:module:import_dll Library vcruntime140.dll not found", want: issueVCRuntime},
		{name: "runtime error takes priority over extension failure", output: "ClientExt.dll failed to load\nLibrary msvcp140.dll not found", want: issueVCRuntime},
		{name: "ClientExt error", output: "ClientExt.dll failed to load", want: issueClientExt},
		{name: "missing native helper", manager: "bottles-native", err: exec.ErrNotFound, want: issueMissingLauncher},
		{name: "missing handoff helper", manager: "steam", err: exec.ErrNotFound, want: issueMissingLauncher},
		{name: "generic failure", err: errors.New("exit status 1"), want: issueGeneric},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLaunchIssue(tc.manager, tc.err, tc.output); got != tc.want {
				t.Fatalf("classifyLaunchIssue() = %q, want %q", got, tc.want)
			}
		})
	}
}
