package schemagen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// errNoModules reports that not one module of the manifest could be resolved,
// which leaves nothing to read a component out of.
var errNoModules = errors.New("no modules could be downloaded")

// resolvedModule is one module of the build list, as the go command reports
// it. The field names are the go command's own, so they are spelled its way.
//
// Dir is where it can be read: the module cache for a downloaded module,
// the checkout itself for one a replacement points at.
//
//nolint:tagliatelle // "go list -m -json" writes Go-style keys, not camelCase
type resolvedModule struct {
	Path    string       `json:"Path"`
	Version string       `json:"Version"`
	Dir     string       `json:"Dir"`
	Main    bool         `json:"Main"`
	Error   *moduleError `json:"Error"`
}

// moduleError is why one module of the build list could not be resolved.
//
//nolint:tagliatelle // the go command's own spelling
type moduleError struct {
	Err string `json:"Err"`
}

// moduleSet is every module of one distribution, by module path. The set holds
// the components the manifest declares and the modules they depend on: a
// component's settings are largely shared config types living in modules of
// their own, so the dependencies are where most field schemas come from.
type moduleSet struct {
	byPath map[string]resolvedModule
}

func (s *moduleSet) lookup(module string) (resolvedModule, bool) {
	m, ok := s.byPath[module]

	return m, ok && m.Dir != ""
}

// resolveModules downloads every module the manifest names, plus everything
// they require, and reports where the go command left them.
//
// The download goes through the go command rather than a fetch of our own so
// that GOPROXY, GOPRIVATE, GONOSUMDB, netrc and any credential helper already
// configured for this machine apply unchanged. A private component resolves
// exactly as it does for the build that consumes it.
func (o *Options) resolveModules(man *manifest) (*moduleSet, error) {
	work, err := o.workspace(man)
	if err != nil {
		return nil, err
	}

	// Tidy resolves the transitive requirements the imports pull in, which is
	// what puts the shared config modules in the build list. It is best effort:
	// a manifest naming a module this machine cannot reach still yields schemas
	// for the ones it can.
	_, err = o.goCommand(work, "mod", "tidy")
	if err != nil {
		o.logf("  go mod tidy: %v\n", err)
	}

	// Downloading fills the module cache; listing is what says where each
	// module ended up, and answers for a replaced module as well, which is
	// never downloaded at all.
	_, err = o.goCommand(work, "mod", "download", "all")
	if err != nil {
		o.logf("  go mod download: %v\n", err)
	}

	out, err := o.goCommand(work, "list", "-m", "-json", "all")
	if err != nil && out == "" {
		return nil, fmt.Errorf("resolve modules: %w", err)
	}

	set := &moduleSet{byPath: map[string]resolvedModule{}}

	dec := json.NewDecoder(strings.NewReader(out))

	for {
		var mod resolvedModule

		decErr := dec.Decode(&mod)
		if errors.Is(decErr, io.EOF) {
			break
		}

		if decErr != nil {
			return nil, fmt.Errorf("read the module list: %w", decErr)
		}

		switch {
		case mod.Main:
			// The workspace itself, which holds nothing to harvest.
		case mod.Error != nil:
			o.logf("  %s: %s\n", mod.Path, firstLine(mod.Error.Err))
		case mod.Dir == "":
			o.logf("  %s: not on disk\n", mod.Path)
		default:
			set.byPath[mod.Path] = mod
		}
	}

	if len(set.byPath) == 0 {
		return nil, errNoModules
	}

	return set, nil
}

// workspace writes the synthetic module the go command resolves in: the
// manifest's components as requirements, its replacements as replacements, and
// a file importing every component so tidy keeps them all.
func (o *Options) workspace(man *manifest) (string, error) {
	dir := filepath.Join(o.cacheDir, "workspace", man.Dist.Name)

	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	requires, err := man.requires()
	if err != nil {
		return "", err
	}

	var gomod bytes.Buffer

	fmt.Fprintf(&gomod, "module schemagen/%s\n\ngo %s\n\nrequire (\n", man.Dist.Name, goDirective())

	for _, req := range requires {
		fmt.Fprintf(&gomod, "\t%s %s\n", req.module, req.version)
	}

	gomod.WriteString(")\n")

	for _, replace := range man.replacements() {
		fmt.Fprintf(&gomod, "\nreplace %s\n", strings.TrimSpace(replace))
	}

	err = os.WriteFile(filepath.Join(dir, "go.mod"), gomod.Bytes(), filePerm)
	if err != nil {
		return "", fmt.Errorf("write go.mod: %w", err)
	}

	var imports bytes.Buffer

	imports.WriteString("//go:build schemagen\n\npackage schemagen\n\nimport (\n")

	for _, req := range requires {
		fmt.Fprintf(&imports, "\t_ %q\n", req.module)
	}

	imports.WriteString(")\n")

	err = os.WriteFile(filepath.Join(dir, "components.go"), imports.Bytes(), filePerm)
	if err != nil {
		return "", fmt.Errorf("write components.go: %w", err)
	}

	return dir, nil
}

// goCommand runs the go tool in the workspace and returns its stdout. The
// environment is inherited: whatever GOPROXY, GOPRIVATE or credentials this
// machine builds with are what a private module needs.
func (o *Options) goCommand(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()

	//nolint:gosec // the arguments are this package's own; only the workspace varies
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		message := stderr.String()
		if message == "" {
			message = err.Error()
		}

		return stdout.String(), fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, firstLine(message))
	}

	return stdout.String(), nil
}

// goDirective is the go directive the workspace declares, taken from the
// toolchain running schemagen so it is never behind what a module requires.
func goDirective() string {
	version := strings.TrimPrefix(runtime.Version(), "go")

	major, rest, found := strings.Cut(version, ".")
	if !found {
		return version
	}

	minor, _, _ := strings.Cut(rest, ".")

	return major + "." + minor
}
