// Copyright 2018 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package runtest

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/syzkaller/pkg/csource"
	"github.com/google/syzkaller/pkg/host"
	"github.com/google/syzkaller/pkg/osutil"
	"github.com/google/syzkaller/prog"
	"github.com/google/syzkaller/sys/targets"
	_ "github.com/google/syzkaller/sys/test/gen" // pull in the test target
)

// Can be used as:
// go test -v -run=Test/64_fork ./pkg/runtest -filter=nonfailing
// to select a subset of tests to run.
var flagFilter = flag.String("filter", "", "prefix to match test file names")

func Test(t *testing.T) {
	switch runtime.GOOS {
	case "openbsd":
		t.Skipf("broken on %v", runtime.GOOS)
	}
	// Test only one target in short mode (each takes 5+ seconds to run).
	shortTarget := targets.Get("test", "64")
	for _, sysTarget := range targets.List["test"] {
		if testing.Short() && sysTarget != shortTarget {
			continue
		}
		sysTarget1 := targets.Get(sysTarget.OS, sysTarget.Arch)
		t.Run(sysTarget1.Arch, func(t *testing.T) {
			t.Parallel()
			test(t, sysTarget1)
		})
	}
}

func test(t *testing.T, sysTarget *targets.Target) {
	target, err := prog.GetTarget(sysTarget.OS, sysTarget.Arch)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := csource.BuildFile(target, filepath.FromSlash("../../executor/executor.cc"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(executor)
	features, err := host.Check(target)
	if err != nil {
		t.Fatalf("failed to detect host features: %v", err)
	}
	calls, _, err := host.DetectSupportedSyscalls(target, "none")
	if err != nil {
		t.Fatalf("failed to detect supported syscalls: %v", err)
	}
	enabledCalls := map[string]map[*prog.Syscall]bool{
		"":     calls,
		"none": calls,
	}
	featureFlags, err := csource.ParseFeaturesFlags("none", "none", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Setup(target, features, featureFlags, executor); err != nil {
		t.Fatal(err)
	}
	requests := make(chan *RunRequest, 2*runtime.GOMAXPROCS(0))
	go func() {
		for req := range requests {
			RunTest(req, executor)
			close(req.Done)
		}
	}()
	ctx := &Context{
		Dir:          filepath.Join("..", "..", "sys", target.OS, "test"),
		Target:       target,
		Tests:        *flagFilter,
		Features:     features,
		EnabledCalls: enabledCalls,
		Requests:     requests,
		LogFunc: func(text string) {
			t.Helper()
			t.Logf(text)
		},
		Retries: 7, // empirical number that seem to reduce flakes to zero
		Verbose: true,
	}
	if err := ctx.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestParsing(t *testing.T) {
	for OS, arches := range targets.List {
		dir := filepath.Join("..", "..", "sys", OS, "test")
		if !osutil.IsExist(dir) {
			continue
		}
		files, err := progFileList(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		for arch := range arches {
			target, err := prog.GetTarget(OS, arch)
			if err != nil {
				t.Fatal(err)
			}
			t.Run(fmt.Sprintf("%v/%v", target.OS, target.Arch), func(t *testing.T) {
				for _, file := range files {
					if _, _, _, err := parseProg(target, dir, file); err != nil {
						t.Errorf("failed to parse %v/%v for %v: %v", dir, file, arch, err)
					}
				}
			})
		}
	}
}
