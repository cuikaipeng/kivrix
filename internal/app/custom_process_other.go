//go:build !darwin && !linux

package app

import "os/exec"

func manageCustomProcess(cmd *exec.Cmd) func() { return func() {} }
