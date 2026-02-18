package dns

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	mdns "github.com/miekg/dns"
	"gopkg.in/yaml.v3"
)

// Config holds all configuration options
type Config struct {
	Listen          string         `yaml:"listen"`
	PublicResolvers []string       `yaml:"public_resolvers"`
	RootServers     []string       `yaml:"root_servers"`
	Defaults        DefaultsConfig `yaml:"defaults"`
}

type DefaultsConfig struct {
	Timeout    string `yaml:"timeout"`
	Retry      string `yaml:"retry"`
	RecordType string `yaml:"record_type"`
}

// DefaultConfig provides sensible defaults for all configuration options.
var DefaultConfig = Config{
	PublicResolvers: []string{
		"1.1.1.1:53",        // Cloudflare
		"8.8.8.8:53",        // Google
		"9.9.9.9:53",        // Quad9
		"208.67.222.222:53", // OpenDNS
		"86.54.11.100:53",   // DNS4EU
		"76.76.2.0:53",      // ControlD
	},
	RootServers: []string{
		"198.41.0.4:53",   // a.root-servers.net
		"199.9.14.201:53", // b.root-servers.net
		"192.33.4.12:53",  // c.root-servers.net
		"199.7.91.13:53",  // d.root-servers.net
	},
	Defaults: DefaultsConfig{
		Timeout:    "1m",
		Retry:      "5s",
		RecordType: "a",
	},
}

// ResolverStatus tracks the propagation state of a single DNS server.
type ResolverStatus struct {
	Name       string
	Addr       string
	Propagated bool
	FoundAt    time.Duration
	Record     string
}

// ServerStatus is the JSON response type for the HTTP API.
type ServerStatus struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	Propagated bool   `json:"propagated"`
	FoundAfter string `json:"found_after,omitempty"`
	Record     string `json:"record,omitempty"`
}

// CheckResponse is the JSON response for a completed DNS propagation check.
type CheckResponse struct {
	Domain        string         `json:"domain"`
	RecordType    string         `json:"record_type"`
	Match         string         `json:"match"`
	Authoritative []ServerStatus `json:"authoritative"`
	Resolvers     []ServerStatus `json:"resolvers"`
	AllPropagated bool           `json:"all_propagated"`
	CheckedAt     string         `json:"checked_at"`
}

// ErrorResponse is the JSON error response for the HTTP API.
type ErrorResponse struct {
	Error string `json:"error"`
}

// SaveConfig writes the config to a YAML file.
func SaveConfig(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadConfig reads a YAML config file and merges it with the provided base config.
func LoadConfig(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var fileConfig Config
	if err := yaml.Unmarshal(data, &fileConfig); err != nil {
		return err
	}

	// Merge with defaults - only override if set in file
	if fileConfig.Listen != "" {
		cfg.Listen = fileConfig.Listen
	}
	if len(fileConfig.PublicResolvers) > 0 {
		cfg.PublicResolvers = fileConfig.PublicResolvers
	}
	if len(fileConfig.RootServers) > 0 {
		cfg.RootServers = fileConfig.RootServers
	}
	if fileConfig.Defaults.Timeout != "" {
		cfg.Defaults.Timeout = fileConfig.Defaults.Timeout
	}
	if fileConfig.Defaults.Retry != "" {
		cfg.Defaults.Retry = fileConfig.Defaults.Retry
	}
	if fileConfig.Defaults.RecordType != "" {
		cfg.Defaults.RecordType = fileConfig.Defaults.RecordType
	}

	return nil
}

// ParseRecordType converts a string record type to a dns library type constant.
func ParseRecordType(t string) uint16 {
	switch strings.ToLower(t) {
	case "a":
		return mdns.TypeA
	case "aaaa":
		return mdns.TypeAAAA
	case "txt":
		return mdns.TypeTXT
	case "cname":
		return mdns.TypeCNAME
	case "mx":
		return mdns.TypeMX
	case "ns":
		return mdns.TypeNS
	default:
		return 0
	}
}

// FindAuthoritativeServers traverses the DNS tree to find all authoritative nameservers for a domain.
func FindAuthoritativeServers(domain string, rootServers []string) ([]*ResolverStatus, error) {
	nsServers := rootServers
	maxDepth := 10

	for depth := 0; depth < maxDepth; depth++ {
		var response *mdns.Msg
		var err error

		for _, ns := range nsServers {
			response, err = QueryDNS(ns, domain, mdns.TypeA)
			if err == nil && response != nil {
				break
			}
		}

		if response == nil {
			return nil, fmt.Errorf("no response from nameservers at depth %d", depth)
		}

		// Check if we got an authoritative answer - we found the authoritative servers
		if response.Authoritative {
			// Query for NS records to get all authoritative nameservers
			return getAuthoritativeNS(domain, nsServers)
		}

		// Look for NS records in the authority section (referral)
		var newNS []string
		var nsNames []string

		for _, rr := range response.Ns {
			if ns, ok := rr.(*mdns.NS); ok {
				nsNames = append(nsNames, ns.Ns)
			}
		}

		// Try to find glue records (A records in additional section)
		glue := make(map[string]string)
		for _, rr := range response.Extra {
			if a, ok := rr.(*mdns.A); ok {
				glue[a.Hdr.Name] = a.A.String()
			}
		}

		// Build list of nameserver IPs
		for _, nsName := range nsNames {
			if ip, ok := glue[nsName]; ok {
				newNS = append(newNS, ip+":53")
			} else {
				ips, err := net.LookupIP(strings.TrimSuffix(nsName, "."))
				if err == nil && len(ips) > 0 {
					newNS = append(newNS, ips[0].String()+":53")
				}
			}
		}

		if len(newNS) == 0 {
			return nil, fmt.Errorf("no more referrals at depth %d", depth)
		}

		nsServers = newNS
	}

	return nil, fmt.Errorf("max depth exceeded")
}

// getAuthoritativeNS queries for NS records and resolves them to IPs.
func getAuthoritativeNS(domain string, currentNS []string) ([]*ResolverStatus, error) {
	var response *mdns.Msg
	var err error

	for _, ns := range currentNS {
		response, err = QueryDNS(ns, domain, mdns.TypeNS)
		if err == nil && response != nil {
			break
		}
	}

	if response == nil {
		// Fall back to using the current NS list
		var result []*ResolverStatus
		for _, ns := range currentNS {
			result = append(result, &ResolverStatus{
				Name: strings.TrimSuffix(ns, ":53"),
				Addr: ns,
			})
		}
		return result, nil
	}

	// Extract NS names from answer section
	var nsNames []string
	for _, rr := range response.Answer {
		if ns, ok := rr.(*mdns.NS); ok {
			nsNames = append(nsNames, ns.Ns)
		}
	}

	// If no NS in answer, check authority section
	if len(nsNames) == 0 {
		for _, rr := range response.Ns {
			if ns, ok := rr.(*mdns.NS); ok {
				nsNames = append(nsNames, ns.Ns)
			}
		}
	}

	// Build glue records map
	glue := make(map[string]string)
	for _, rr := range response.Extra {
		if a, ok := rr.(*mdns.A); ok {
			glue[a.Hdr.Name] = a.A.String()
		}
	}

	// Resolve each NS to IP and create status entries
	var result []*ResolverStatus
	for _, nsName := range nsNames {
		var ip string
		if glueIP, ok := glue[nsName]; ok {
			ip = glueIP
		} else {
			ips, err := net.LookupIP(strings.TrimSuffix(nsName, "."))
			if err != nil || len(ips) == 0 {
				continue
			}
			ip = ips[0].String()
		}

		result = append(result, &ResolverStatus{
			Name: strings.TrimSuffix(nsName, "."),
			Addr: ip + ":53",
		})
	}

	if len(result) == 0 {
		// Fall back to current NS list
		for _, ns := range currentNS {
			result = append(result, &ResolverStatus{
				Name: strings.TrimSuffix(ns, ":53"),
				Addr: ns,
			})
		}
	}

	return result, nil
}

// QueryDNS sends a DNS query to a specific server.
func QueryDNS(server, domain string, qtype uint16) (*mdns.Msg, error) {
	c := new(mdns.Client)
	c.Timeout = 5 * time.Second

	m := new(mdns.Msg)
	m.SetQuestion(domain, qtype)
	m.RecursionDesired = false

	r, _, err := c.Exchange(m, server)
	if err != nil {
		return nil, err
	}

	return r, nil
}

// QueryAuthoritativeRecord checks a single authoritative server for a matching record.
func QueryAuthoritativeRecord(server, domain string, qtype uint16, match string) string {
	response, err := QueryDNS(server, domain, qtype)
	if err != nil || response == nil {
		return ""
	}

	return MatchRecord(response.Answer, qtype, match)
}

// MatchRecord checks DNS answer records for a match.
func MatchRecord(answers []mdns.RR, qtype uint16, match string) string {
	for _, rr := range answers {
		switch qtype {
		case mdns.TypeA:
			if a, ok := rr.(*mdns.A); ok {
				if a.A.String() == match {
					return fmt.Sprintf("A %s", a.A.String())
				}
			}
		case mdns.TypeAAAA:
			if aaaa, ok := rr.(*mdns.AAAA); ok {
				if aaaa.AAAA.String() == match {
					return fmt.Sprintf("AAAA %s", aaaa.AAAA.String())
				}
			}
		case mdns.TypeTXT:
			if txt, ok := rr.(*mdns.TXT); ok {
				joined := strings.Join(txt.Txt, "")
				if strings.Contains(joined, match) {
					return joined
				}
			}
		case mdns.TypeCNAME:
			if cname, ok := rr.(*mdns.CNAME); ok {
				if strings.Contains(cname.Target, match) {
					return fmt.Sprintf("CNAME %s", cname.Target)
				}
			}
		case mdns.TypeMX:
			if mx, ok := rr.(*mdns.MX); ok {
				if strings.Contains(mx.Mx, match) {
					return fmt.Sprintf("MX %s (pref %d)", mx.Mx, mx.Preference)
				}
			}
		}
	}
	return ""
}

// CheckResolver checks a single resolver for a matching record.
func CheckResolver(addr, domain, recordType, match string) (string, bool) {
	var resolver *net.Resolver
	if addr == "" {
		resolver = net.DefaultResolver
	} else {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "udp", addr)
			},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch strings.ToLower(recordType) {
	case "a":
		return checkA(ctx, resolver, domain, match)
	case "txt":
		return checkTXT(ctx, resolver, domain, match)
	case "cname":
		return checkCNAME(ctx, resolver, domain, match)
	case "mx":
		return checkMX(ctx, resolver, domain, match)
	default:
		return "", false
	}
}

func checkA(ctx context.Context, resolver *net.Resolver, domain, match string) (string, bool) {
	ips, err := resolver.LookupIP(ctx, "ip4", domain)
	if err != nil {
		return "", false
	}
	for _, ip := range ips {
		if ip.String() == match {
			return fmt.Sprintf("A %s", ip.String()), true
		}
	}
	return "", false
}

func checkTXT(ctx context.Context, resolver *net.Resolver, domain, match string) (string, bool) {
	records, err := resolver.LookupTXT(ctx, domain)
	if err != nil {
		return "", false
	}
	for _, record := range records {
		if strings.Contains(record, match) {
			return record, true
		}
	}
	return "", false
}

func checkCNAME(ctx context.Context, resolver *net.Resolver, domain, match string) (string, bool) {
	cname, err := resolver.LookupCNAME(ctx, domain)
	if err != nil {
		return "", false
	}
	if strings.Contains(cname, match) {
		return fmt.Sprintf("CNAME %s", cname), true
	}
	return "", false
}

func checkMX(ctx context.Context, resolver *net.Resolver, domain, match string) (string, bool) {
	mxs, err := resolver.LookupMX(ctx, domain)
	if err != nil {
		return "", false
	}
	for _, mx := range mxs {
		if strings.Contains(mx.Host, match) {
			return fmt.Sprintf("MX %s (pref %d)", mx.Host, mx.Pref), true
		}
	}
	return "", false
}

// FormatDuration formats a duration for display.
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	if secs == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm%ds", mins, secs)
}

// CheckPropagation runs a full DNS propagation check and returns the result.
func CheckPropagation(cfg *Config, domain, recordType, match string, dnsType uint16, timeout, retry time.Duration) (*CheckResponse, error) {
	// Find authoritative nameservers
	authServers, err := FindAuthoritativeServers(domain, cfg.RootServers)
	if err != nil {
		return nil, fmt.Errorf("failed to find authoritative servers: %w", err)
	}

	// Build public resolver list
	resolvers := make([]*ResolverStatus, 0, len(cfg.PublicResolvers)+1)
	for _, addr := range cfg.PublicResolvers {
		resolvers = append(resolvers, &ResolverStatus{
			Name: strings.Split(addr, ":")[0],
			Addr: addr,
		})
	}
	resolvers = append(resolvers, &ResolverStatus{
		Name: "local",
		Addr: "",
	})

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var mu sync.Mutex
	startTime := time.Now()

	// Check with retries until timeout
	ticker := time.NewTicker(retry)
	defer ticker.Stop()

	for {
		// Check authoritative servers
		CheckAuthoritativeAllSilent(authServers, domain, dnsType, match, startTime, &mu)

		// Check resolvers
		CheckResolverAllSilent(resolvers, domain, recordType, match, startTime, &mu)

		// Check if all propagated
		allDone := true
		mu.Lock()
		for _, s := range authServers {
			if !s.Propagated {
				allDone = false
				break
			}
		}
		if allDone {
			for _, r := range resolvers {
				if !r.Propagated {
					allDone = false
					break
				}
			}
		}
		mu.Unlock()

		if allDone {
			break
		}

		select {
		case <-ctx.Done():
			// Timeout - return current state
			goto buildResponse
		case <-ticker.C:
			continue
		}
	}

buildResponse:
	// Build response
	response := &CheckResponse{
		Domain:        strings.TrimSuffix(domain, "."),
		RecordType:    strings.ToUpper(recordType),
		Match:         match,
		Authoritative: make([]ServerStatus, 0, len(authServers)),
		Resolvers:     make([]ServerStatus, 0, len(resolvers)),
		AllPropagated: true,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	mu.Lock()
	for _, s := range authServers {
		status := ServerStatus{
			Name:       s.Name,
			Address:    strings.TrimSuffix(s.Addr, ":53"),
			Propagated: s.Propagated,
		}
		if s.Propagated {
			status.FoundAfter = FormatDuration(s.FoundAt)
			status.Record = s.Record
		} else {
			response.AllPropagated = false
		}
		response.Authoritative = append(response.Authoritative, status)
	}

	for _, r := range resolvers {
		status := ServerStatus{
			Name:       r.Name,
			Address:    r.Addr,
			Propagated: r.Propagated,
		}
		if r.Addr == "" {
			status.Address = "system"
		} else {
			status.Address = strings.TrimSuffix(r.Addr, ":53")
		}
		if r.Propagated {
			status.FoundAfter = FormatDuration(r.FoundAt)
			status.Record = r.Record
		} else {
			response.AllPropagated = false
		}
		response.Resolvers = append(response.Resolvers, status)
	}
	mu.Unlock()

	return response, nil
}

// CheckAuthoritativeAllSilent checks all authoritative servers without printing output.
func CheckAuthoritativeAllSilent(servers []*ResolverStatus, domain string, qtype uint16, match string, startTime time.Time, mu *sync.Mutex) {
	var wg sync.WaitGroup
	for _, s := range servers {
		mu.Lock()
		if s.Propagated {
			mu.Unlock()
			continue
		}
		mu.Unlock()

		wg.Add(1)
		go func(s *ResolverStatus) {
			defer wg.Done()
			record := QueryAuthoritativeRecord(s.Addr, domain, qtype, match)
			if record != "" {
				mu.Lock()
				if !s.Propagated {
					s.Propagated = true
					s.FoundAt = time.Since(startTime)
					s.Record = record
				}
				mu.Unlock()
			}
		}(s)
	}
	wg.Wait()
}

// CheckResolverAllSilent checks all resolvers without printing output.
func CheckResolverAllSilent(resolvers []*ResolverStatus, domain, recordType, match string, startTime time.Time, mu *sync.Mutex) {
	var wg sync.WaitGroup
	for _, r := range resolvers {
		mu.Lock()
		if r.Propagated {
			mu.Unlock()
			continue
		}
		mu.Unlock()

		wg.Add(1)
		go func(r *ResolverStatus) {
			defer wg.Done()
			record, found := CheckResolver(r.Addr, strings.TrimSuffix(domain, "."), recordType, match)
			if found {
				mu.Lock()
				if !r.Propagated {
					r.Propagated = true
					r.FoundAt = time.Since(startTime)
					r.Record = record
				}
				mu.Unlock()
			}
		}(r)
	}
	wg.Wait()
}

// CheckAuthoritativeAllVerbose checks authoritative servers with printed output (for CLI mode).
func CheckAuthoritativeAllVerbose(servers []*ResolverStatus, domain string, qtype uint16, match, recordType string, startTime time.Time, mu *sync.Mutex) {
	var wg sync.WaitGroup
	for _, s := range servers {
		mu.Lock()
		if s.Propagated {
			mu.Unlock()
			continue
		}
		mu.Unlock()

		wg.Add(1)
		go func(s *ResolverStatus) {
			defer wg.Done()
			record := QueryAuthoritativeRecord(s.Addr, domain, qtype, match)
			if record != "" {
				mu.Lock()
				if !s.Propagated {
					s.Propagated = true
					s.FoundAt = time.Since(startTime)
					s.Record = record
					fmt.Printf(" - %s authoritative %s has record %s (%s)\n",
						FormatDuration(s.FoundAt), s.Name, strings.ToUpper(recordType), record)
				}
				mu.Unlock()
			}
		}(s)
	}
	wg.Wait()
}

// CheckResolverAllVerbose checks resolvers with printed output (for CLI mode).
func CheckResolverAllVerbose(resolvers []*ResolverStatus, domain, recordType, match string, startTime time.Time, mu *sync.Mutex) {
	var wg sync.WaitGroup
	for _, r := range resolvers {
		mu.Lock()
		if r.Propagated {
			mu.Unlock()
			continue
		}
		mu.Unlock()

		wg.Add(1)
		go func(r *ResolverStatus) {
			defer wg.Done()
			record, found := CheckResolver(r.Addr, strings.TrimSuffix(domain, "."), recordType, match)
			if found {
				mu.Lock()
				if !r.Propagated {
					r.Propagated = true
					r.FoundAt = time.Since(startTime)
					r.Record = record
					fmt.Printf(" - %s resolver %s propagated record %s (%s)\n",
						FormatDuration(r.FoundAt), r.Name, strings.ToUpper(recordType), record)
				}
				mu.Unlock()
			}
		}(r)
	}
	wg.Wait()
}

// PrintSummary prints a summary of server propagation status.
func PrintSummary(servers []*ResolverStatus, serverType string) {
	fmt.Printf("\nSummary (%s):\n", serverType)
	for _, s := range servers {
		if s.Propagated {
			fmt.Printf(" - %s: propagated at %s (%s)\n", s.Name, FormatDuration(s.FoundAt), s.Record)
		} else {
			fmt.Printf(" - %s: NOT propagated\n", s.Name)
		}
	}
}
