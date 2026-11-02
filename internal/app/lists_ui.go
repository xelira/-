package app

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ampersand/internal/config"
)

type ListsView struct {
	Domains        []string `json:"domains"`
	IPs            []string `json:"ips"`
	BuiltinDomains []string `json:"builtinDomains"`
	BuiltinIPs     []string `json:"builtinIPs"`
}

func (m *Manager) CustomLists() ListsView {
	cfg := m.store.Snapshot()
	view := ListsView{
		BuiltinDomains: readList(filepath.Join(m.resources, "engines", "lists", "list-general-user.txt")),
		BuiltinIPs:     readList(filepath.Join(m.resources, "engines", "lists", "ip-list.default")),
	}
	for _, entry := range cfg.DPI.CustomAddresses {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if isIPListEntry(entry) {
			view.IPs = append(view.IPs, entry)
		} else {
			view.Domains = append(view.Domains, entry)
		}
	}
	return view
}

func (m *Manager) SaveCustomLists(domains, ips []string) ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	for _, value := range append(append([]string{}, domains...), ips...) {
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		if err := validateListEntry(value); err != nil {
			return ActionResult{OK: false, Message: fmt.Sprintf("Проверка списка: %q — %v", value, err), State: m.state()}
		}
	}
	entries := make([]string, 0, len(domains)+len(ips))
	seen := map[string]bool{}
	for _, value := range append(append([]string{}, domains...), ips...) {
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(value, "#") || seen[value] {
			continue
		}
		seen[value] = true
		entries = append(entries, value)
	}
	if len(entries) > 500 {
		return ActionResult{OK: false, Message: "Слишком много записей: максимум 500", State: m.state()}
	}
	err := m.store.Update(func(c *config.Config) error {
		c.DPI.CustomAddresses = entries
		return nil
	})
	if err != nil {
		return ActionResult{OK: false, Message: err.Error(), State: m.state()}
	}
	msg := "Списки сохранены"
	if m.dpi.Running() {
		msg += ". Перезапустите DPI, чтобы применить изменения"
	}
	m.emitState()
	return ActionResult{OK: true, Message: msg, State: m.state()}
}

func readList(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

func isIPListEntry(value string) bool {
	if net.ParseIP(value) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(value)
	return err == nil
}

var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validateListEntry(value string) error {
	if isIPListEntry(value) {
		return nil
	}
	if strings.ContainsAny(value, " \t\r\n/\\:@?#") {
		return fmt.Errorf("это не домен и не IP/CIDR")
	}
	host := strings.TrimPrefix(strings.ToLower(value), "*.")
	if host == "" || len(host) > 253 {
		return fmt.Errorf("некорректная длина домена")
	}
	// A numeric dotted value is a broken IP, not a domain.
	if strings.Trim(host, "0123456789.") == "" {
		return fmt.Errorf("некорректный IP-адрес")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !domainLabel.MatchString(label) {
			return fmt.Errorf("некорректная метка домена %q", label)
		}
	}
	return nil
}
