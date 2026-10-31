// Пакет profiles читает зафиксированные DPI-профили с указанием источника.
package profiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const SchemaVersion = 1

var (
	ErrUnsupportedPlatform = errors.New("profile does not support this platform")
	ErrInvalidProfile      = errors.New("invalid profile")
)

type Catalog struct {
	SchemaVersion int       `json:"schemaVersion"`
	Profiles      []Profile `json:"profiles"`
}

type Profile struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Platforms   []string             `json:"platforms"`
	Engines     map[string]EngineRef `json:"engines"`
	Ports       Ports                `json:"ports"`
	Windows     PlatformArgs         `json:"windows"`
	Linux       PlatformArgs         `json:"linux"`
	Source      Source               `json:"source"`
}

type EngineRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type Ports struct {
	TCP []string `json:"tcp"`
	UDP []string `json:"udp"`
}

type PlatformArgs struct {
	WFTCP string   `json:"wfTcp,omitempty"`
	WFUDP string   `json:"wfUdp,omitempty"`
	Args  []string `json:"args"`
}

type Source struct {
	Repository      string   `json:"repository"`
	Revision        string   `json:"revision"`
	Files           []string `json:"files"`
	License         string   `json:"license"`
	Upstream        string   `json:"upstream"`
	UpstreamVersion string   `json:"upstreamVersion"`
	Notes           string   `json:"notes"`
}

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
	portPattern = regexp.MustCompile(`^[0-9]{1,5}(-[0-9]{1,5})?$`)
)

func Load(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profiles: %w", err)
	}
	var c Catalog
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode profiles: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalog) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema %d", ErrInvalidProfile, c.SchemaVersion)
	}
	if len(c.Profiles) == 0 {
		return fmt.Errorf("%w: catalog is empty", ErrInvalidProfile)
	}
	seen := map[string]bool{}
	for i, p := range c.Profiles {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("profile %d: %w", i, err)
		}
		if seen[p.ID] {
			return fmt.Errorf("%w: duplicate id %q", ErrInvalidProfile, p.ID)
		}
		seen[p.ID] = true
	}
	return nil
}

func (p Profile) Validate() error {
	if !idPattern.MatchString(p.ID) {
		return fmt.Errorf("%w: bad id %q", ErrInvalidProfile, p.ID)
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Description) == "" {
		return fmt.Errorf("%w: %s has empty name or description", ErrInvalidProfile, p.ID)
	}
	if len(p.Platforms) == 0 {
		return fmt.Errorf("%w: %s has no platforms", ErrInvalidProfile, p.ID)
	}
	for _, platform := range p.Platforms {
		if platform != "windows" && platform != "linux" {
			return fmt.Errorf("%w: %s has unknown platform %q", ErrInvalidProfile, p.ID, platform)
		}
		ref, ok := p.Engines[platform]
		if !ok || ref.ID == "" || ref.Version == "" {
			return fmt.Errorf("%w: %s has no engine metadata for %s", ErrInvalidProfile, p.ID, platform)
		}
		args := p.ArgsFor(platform)
		if len(args.Args) == 0 {
			return fmt.Errorf("%w: %s has no arguments for %s", ErrInvalidProfile, p.ID, platform)
		}
		for _, arg := range args.Args {
			if err := validateArg(arg); err != nil {
				return fmt.Errorf("%w: %s/%s: %v", ErrInvalidProfile, p.ID, platform, err)
			}
		}
	}
	if len(p.Ports.TCP) == 0 && len(p.Ports.UDP) == 0 {
		return fmt.Errorf("%w: %s has no packet ports", ErrInvalidProfile, p.ID)
	}
	for _, pnum := range append(append([]string{}, p.Ports.TCP...), p.Ports.UDP...) {
		if !portPattern.MatchString(pnum) {
			return fmt.Errorf("%w: %s has bad port selector %q", ErrInvalidProfile, p.ID, pnum)
		}
	}
	if p.Source.Repository == "" || p.Source.Revision == "" || len(p.Source.Files) == 0 {
		return fmt.Errorf("%w: %s lacks source attribution", ErrInvalidProfile, p.ID)
	}
	return nil
}

func validateArg(arg string) error {
	if arg == "" {
		return errors.New("empty argument")
	}
	if strings.ContainsAny(arg, "\x00\r\n") {
		return errors.New("control characters are forbidden")
	}
	if !strings.HasPrefix(arg, "--") && !strings.HasPrefix(arg, "{") {
		return fmt.Errorf("argument does not look like a flag: %q", arg)
	}
	return nil
}

func (p Profile) Supports(goos string) bool { return slices.Contains(p.Platforms, goos) }

func (p Profile) ArgsFor(goos string) PlatformArgs {
	switch goos {
	case "windows":
		return p.Windows
	case "linux":
		return p.Linux
	default:
		return PlatformArgs{}
	}
}

func (p Profile) EngineFor(goos string) (EngineRef, bool) {
	r, ok := p.Engines[goos]
	return r, ok
}

// Expand подставляет только известные плейсхолдеры. Никакого shell и разбора
// кавычек — аргументы идут в exec как есть.
func Expand(args []string, root, bin, lists string) []string {
	out := make([]string, len(args))
	repl := strings.NewReplacer(
		"{root}", root,
		"{bin}", bin,
		"{lists}", lists,
	)
	for i, arg := range args {
		out[i] = repl.Replace(arg)
	}
	return out
}

func (c *Catalog) Find(id string) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// EnginePaths отдаёт стандартные пути внутри resources/engines.
func EnginePaths(resources string, goos string) (root, bin, lists string) {
	root = filepath.Join(resources, "engines", goos)
	bin = filepath.Join(resources, "engines", "bin")
	lists = filepath.Join(resources, "engines", "lists")
	return
}
