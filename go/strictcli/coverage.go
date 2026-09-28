package strictcli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// recordCoverage appends a coverage record for the resolved command path.
// Each Test() or Call() invocation appends one JSONL line to the process's
// shard file (named "<pid>.jsonl"). Uniqueness across concurrent writers comes
// from the PID and O_APPEND; one shard per process is sufficient, so there is
// no per-write shard counter.
func (a *App) recordCoverage(cmdPath string) {
	if a.coverageShardPath == "" {
		return
	}
	path := a.coverageShardPath
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(map[string]string{"command": cmdPath})
	f.Write(data)
	f.Write([]byte("\n"))
	a.recordCacheWrite(path)
}

// collectAllCommandPaths enumerates all non-deprecated leaf command paths
// as dot-separated strings (e.g. "deploy", "infra.deploy").
func (a *App) collectAllCommandPaths() map[string]bool {
	paths := make(map[string]bool)
	for name := range a.commands {
		paths[name] = true
	}
	var walkGroup func(grp *Group, prefix []string)
	walkGroup = func(grp *Group, prefix []string) {
		for cmdName := range grp.Commands {
			paths[strings.Join(append(prefix, cmdName), ".")] = true
		}
		for subName, subGrp := range grp.Groups {
			walkGroup(subGrp, append(prefix, subName))
		}
	}
	for groupName, grp := range a.groups {
		walkGroup(grp, []string{groupName})
	}
	return paths
}

// testCoverageProvider is the built-in check provider for cli-test-coverage.
// Auto-registered when WithTestCoverageDir() names a directory that exists.
//
// The verdict is derived from committed state: the covered set is the union of
// the committed manifest (test-coverage.json in the declared directory) and any
// per-process shard files merged from its coverage/ subdirectory. Every live
// registered command path (minus the injected check command) must be present in
// that union to pass; otherwise the check fails naming each uncovered command.
//
// Because the verdict reads the committed manifest, it is deterministic on every
// machine -- a machine that never ran the suite (no local shards) still gets a
// stable verdict from the committed manifest alone. Both the coverage dir and
// the manifest path sit under the DECLARED directory, so the check evaluated
// from any cwd reads the app's own repo state.
//
// The manifest is rewritten as the monotonic union of its prior contents and
// the freshly merged shards, but ONLY when that content actually changes -- a
// pure check must not dirty a byte-identical file. Accepted staleness: deleting
// a test leaves its command covered in the manifest until the manifest is
// deliberately regenerated (e.g. by removing it and re-running the suite),
// because the union never removes a command.
func (a *App) testCoverageProvider() []CheckSpec {
	impl := func(ctx CheckContext, reporter *ErrorReporter) CheckOutcome {
		coverageDir := a.coverageDir
		manifestPath := a.coverageManifestPath

		// Subject-matter gating (the sanctioned skip class, mirroring project-type
		// gating): when the declared coverage root holds NEITHER a committed
		// manifest NOR any shard files, the suite has never run against it.
		// Report a visible SKIP instead of failing with the app's entire command
		// surface listed as uncovered. When EITHER exists, behavior is unchanged:
		// a partial manifest still fails honestly, and an empty-manifest file
		// present still means "coverage configured but empty" = fail listing all.
		manifestExists := false
		if manifestPath != "" {
			if info, err := os.Stat(manifestPath); err == nil && !info.IsDir() {
				manifestExists = true
			}
		}
		shardsExist := false
		if entries, err := os.ReadDir(coverageDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
					shardsExist = true
					break
				}
			}
		}
		if !manifestExists && !shardsExist {
			return reporter.Skipped(fmt.Sprintf(
				"no coverage state at %s -- cli-test-coverage applies to the app's own development tree",
				filepath.Dir(manifestPath)))
		}

		covered := make(map[string]bool)

		// Seed from the committed manifest -- this is what makes the verdict
		// deterministic on machines that never ran the suite.
		if manifestPath != "" {
			if raw, err := os.ReadFile(manifestPath); err == nil {
				var manifestCmds []string
				if err := json.Unmarshal(raw, &manifestCmds); err == nil {
					for _, cmd := range manifestCmds {
						covered[cmd] = true
					}
				}
			}
		}

		// Merge shards (optional freshness input)
		entries, err := os.ReadDir(coverageDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
					continue
				}
				fpath := filepath.Join(coverageDir, entry.Name())
				f, err := os.Open(fpath)
				if err != nil {
					continue
				}
				scanner := bufio.NewScanner(f)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line == "" {
						continue
					}
					var record map[string]string
					if err := json.Unmarshal([]byte(line), &record); err != nil {
						continue
					}
					if cmd, ok := record["command"]; ok {
						covered[cmd] = true
					}
				}
				f.Close()
			}
		}

		// Rewrite the manifest as the monotonic union, but only when the content
		// actually changes (keeps a pure check from dirtying a byte-identical
		// file).
		if manifestPath != "" && len(covered) > 0 {
			manifest := make([]string, 0, len(covered))
			for cmd := range covered {
				manifest = append(manifest, cmd)
			}
			sort.Strings(manifest)
			data, _ := json.MarshalIndent(manifest, "", "  ")
			newContent := append(data, '\n')
			existing, _ := os.ReadFile(manifestPath)
			if !bytes.Equal(existing, newContent) {
				os.MkdirAll(filepath.Dir(manifestPath), 0o755)
				os.WriteFile(manifestPath, newContent, 0o644)
				a.recordCacheWrite(manifestPath)
			}
		}

		// Compare against command surface (exclude the framework-injected
		// check commands -- they are not user commands)
		allCommands := a.collectAllCommandPaths()
		delete(allCommands, "check")
		delete(allCommands, "failing-checks")
		var uncovered []string
		for cmd := range allCommands {
			if !covered[cmd] {
				uncovered = append(uncovered, cmd)
			}
		}
		sort.Strings(uncovered)

		if len(uncovered) > 0 {
			for _, cmd := range uncovered {
				reporter.Error(fmt.Sprintf("no test coverage for command: %s", cmd))
			}
			return reporter.Found(fmt.Sprintf("%d command(s) with zero test coverage", len(uncovered)))
		}
		return reporter.Passed(fmt.Sprintf("all %d commands have test coverage", len(allCommands)))
	}

	return []CheckSpec{
		NewErrorCheckSpec(CheckSpecMeta{
			Name:         "cli-test-coverage",
			Tags:         []string{"test"},
			Severity:     "error",
			Fast:         true,
			Pure:         true,
			NeedsNetwork: false,
			DependsOn:    []string{},
		}, impl),
	}
}
