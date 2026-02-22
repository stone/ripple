package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	dnspkg "ripple/dns"
	"ripple/tui"
)

// Global config loaded at startup
var config = dnspkg.DefaultConfig

func main() {
	configFile := flag.String("c", "", "config file path (YAML)")
	retryInterval := flag.String("r", "", "retry interval (default from config or 5s)")
	duration := flag.String("w", "", "how long to run (default from config or 1m)")
	recordType := flag.String("t", "", "record type (a, txt, cname, mx)")
	match := flag.String("m", "", "match value in record")
	serve := flag.String("serve", "", "start HTTP server on address (e.g., :8080)")
	tuiMode := flag.Bool("tui", false, "launch interactive terminal UI")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <domain>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "       %s -serve :8080\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "       %s -tui\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nCLI Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s -t txt -m _varnish-cdn-verify lab.varnish.cloud\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -t a -m 127.0.0.1 lab.varnish.cloud\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -t txt -m v=spf1 -w 30s -r 3s google.com\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -t a -m 1.1.1.1 -w 30s -r 3s one.one.one.one\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nHTTP Server Mode:\n")
		fmt.Fprintf(os.Stderr, "  %s -serve :8080\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nHTTP API Endpoints:\n")
		fmt.Fprintf(os.Stderr, "  GET  /health                                    - Health check\n")
		fmt.Fprintf(os.Stderr, "  GET  /check?domain=<d>&type=<t>&match=<m>       - One-shot check\n")
		fmt.Fprintf(os.Stderr, "  POST /check {domain,type,match,timeout,retry}   - Check with retries\n")
		fmt.Fprintf(os.Stderr, "\nConfig File:\n")
		fmt.Fprintf(os.Stderr, "  %s -c config.yaml -serve :8080\n", os.Args[0])
	}
	flag.Parse()

	// Load config file if specified
	if *configFile != "" {
		if err := dnspkg.LoadConfig(&config, *configFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config file: %v\n", err)
			os.Exit(1)
		}
	}

	// Apply defaults from config, override with CLI flags
	retryDuration, _ := time.ParseDuration(config.Defaults.Retry)
	if *retryInterval != "" {
		retryDuration, _ = time.ParseDuration(*retryInterval)
	}

	timeoutDuration, _ := time.ParseDuration(config.Defaults.Timeout)
	if *duration != "" {
		timeoutDuration, _ = time.ParseDuration(*duration)
	}

	recType := config.Defaults.RecordType
	if *recordType != "" {
		recType = *recordType
	}

	// TUI mode
	if *tuiMode {
		m := tui.New(&config, *configFile)
		p := tea.NewProgram(m, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Determine listen address (CLI flag overrides config)
	listenAddr := config.Listen
	if *serve != "" {
		listenAddr = *serve
	}

	// HTTP Server mode
	if listenAddr != "" {
		runServer(listenAddr)
		return
	}

	// CLI mode
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	domain := flag.Arg(0)
	if *match == "" {
		fmt.Fprintf(os.Stderr, "Error: -m (match) is required\n")
		os.Exit(1)
	}

	runCLI(domain, recType, *match, retryDuration, timeoutDuration)
}

func runServer(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleUI)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/check", handleCheck)
	mux.HandleFunc("/check/stream", handleCheckStream)

	handler := accessLog(mux)

	log.Printf("Starting HTTP server on %s", addr)
	log.Printf("UI available at http://localhost%s", addr)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}

// Implement http.Flusher for SSE support
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// accessLog middleware logs all HTTP requests
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rw := &responseWriter{ResponseWriter: w, status: 0}
		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}

		clientIP := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			clientIP = strings.Split(xff, ",")[0]
		}

		log.Printf("%s %s %s %d %s",
			clientIP,
			r.Method,
			r.URL.Path,
			status,
			duration.Round(time.Millisecond),
		)
	})
}

func handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(uiHTML))
}

const uiHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>DNS Propagation Checker</title>
    <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            max-width: 900px;
            margin: 0 auto;
            padding: 20px;
            background: #f5f5f5;
        }
        h1 { color: #333; margin-bottom: 5px; }
        .subtitle { color: #666; margin-bottom: 20px; }
        .card {
            background: white;
            border-radius: 8px;
            padding: 20px;
            margin-bottom: 20px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        .form-row {
            display: flex;
            gap: 10px;
            flex-wrap: wrap;
            margin-bottom: 15px;
        }
        .form-group {
            display: flex;
            flex-direction: column;
            flex: 1;
            min-width: 150px;
        }
        label {
            font-size: 12px;
            color: #666;
            margin-bottom: 4px;
            font-weight: 500;
        }
        input, select {
            padding: 10px;
            border: 1px solid #ddd;
            border-radius: 4px;
            font-size: 14px;
        }
        input:focus, select:focus {
            outline: none;
            border-color: #007bff;
        }
        button {
            background: #007bff;
            color: white;
            border: none;
            padding: 12px 24px;
            border-radius: 4px;
            cursor: pointer;
            font-size: 14px;
            font-weight: 500;
        }
        button:hover { background: #0056b3; }
        button:disabled {
            background: #ccc;
            cursor: not-allowed;
        }
        .btn-cancel {
            background: #dc3545;
        }
        .btn-cancel:hover {
            background: #c82333;
        }
        .section-title {
            font-size: 14px;
            font-weight: 600;
            color: #333;
            margin-bottom: 10px;
            padding-bottom: 5px;
            border-bottom: 1px solid #eee;
        }
        .server-list {
            display: grid;
            gap: 8px;
        }
        .server-item {
            display: flex;
            align-items: center;
            padding: 10px;
            background: #f8f9fa;
            border-radius: 4px;
            font-size: 13px;
        }
        .status-icon {
            width: 20px;
            height: 20px;
            border-radius: 50%;
            margin-right: 10px;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 12px;
        }
        .status-ok { background: #28a745; color: white; }
        .status-pending { background: #ffc107; color: #333; }
        .status-waiting { background: #6c757d; color: white; }
        .server-name { font-weight: 500; min-width: 180px; }
        .server-addr { color: #666; min-width: 120px; }
        .server-time { color: #28a745; min-width: 60px; }
        .server-record {
            color: #666;
            font-family: monospace;
            font-size: 12px;
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
        }
        .error {
            background: #f8d7da;
            color: #721c24;
            padding: 10px;
            border-radius: 4px;
            margin-bottom: 15px;
        }
        .all-done {
            background: #d4edda;
            color: #155724;
            padding: 15px;
            border-radius: 4px;
            text-align: center;
            font-weight: 500;
        }
        .loading {
            display: inline-block;
            width: 16px;
            height: 16px;
            border: 2px solid #fff;
            border-top-color: transparent;
            border-radius: 50%;
            animation: spin 1s linear infinite;
            margin-right: 8px;
        }
        @keyframes spin { to { transform: rotate(360deg); } }
        .meta {
            font-size: 12px;
            color: #666;
            margin-top: 10px;
        }
    </style>
</head>
<body>
    <h1>DNS Propagation Checker</h1>
    <p class="subtitle">Check DNS record propagation across authoritative nameservers and public resolvers</p>

    <div x-data="dnsChecker()" class="card">
        <form @submit.prevent="runCheck">
            <div class="form-row">
                <div class="form-group" style="flex: 2;">
                    <label>Domain</label>
                    <input type="text" x-model="domain" placeholder="example.com" required>
                </div>
                <div class="form-group" style="flex: 0.7;">
                    <label>Record Type</label>
                    <select x-model="recordType">
                        <option value="a">A</option>
                        <option value="txt">TXT</option>
                        <option value="cname">CNAME</option>
                        <option value="mx">MX</option>
                        <option value="aaaa">AAAA</option>
                    </select>
                </div>
                <div class="form-group" style="flex: 1.5;">
                    <label>Match Value</label>
                    <input type="text" x-model="match" placeholder="1.2.3.4 or text to match" required>
                </div>
            </div>
            <div class="form-row">
                <div class="form-group" style="flex: 0.5;">
                    <label>Timeout</label>
                    <input type="text" x-model="timeout" placeholder="1m">
                </div>
                <div class="form-group" style="flex: 0.5;">
                    <label>Retry Interval</label>
                    <input type="text" x-model="retry" placeholder="5s">
                </div>
                <div class="form-group" style="flex: 1; justify-content: flex-end; flex-direction: row; align-items: flex-end; gap: 10px;">
                    <button type="submit" :disabled="loading">
                        <span x-show="loading" class="loading"></span>
                        <span x-text="loading ? 'Checking...' : 'Check Propagation'"></span>
                    </button>
                    <button type="button" x-show="loading" @click="cancelCheck" class="btn-cancel">
                        Cancel
                    </button>
                </div>
            </div>
        </form>

        <template x-if="error">
            <div class="error" x-text="error"></div>
        </template>

        <template x-if="result">
            <div>
                <template x-if="result.all_propagated">
                    <div class="all-done">All servers have propagated the record!</div>
                </template>

                <div style="margin-top: 20px;">
                    <div class="section-title">Authoritative Nameservers</div>
                    <div class="server-list">
                        <template x-for="server in result.authoritative" :key="server.name">
                            <div class="server-item">
                                <div class="status-icon" :class="server.propagated ? 'status-ok' : 'status-pending'"
                                     x-text="server.propagated ? '✓' : '○'"></div>
                                <span class="server-name" x-text="server.name"></span>
                                <span class="server-addr" x-text="server.address"></span>
                                <span class="server-time" x-text="server.propagated ? server.found_after : '-'"></span>
                                <span class="server-record" x-text="server.record || '-'" :title="server.record"></span>
                            </div>
                        </template>
                    </div>
                </div>

                <div style="margin-top: 20px;">
                    <div class="section-title">Public Resolvers</div>
                    <div class="server-list">
                        <template x-for="server in result.resolvers" :key="server.name">
                            <div class="server-item">
                                <div class="status-icon" :class="server.propagated ? 'status-ok' : 'status-pending'"
                                     x-text="server.propagated ? '✓' : '○'"></div>
                                <span class="server-name" x-text="server.name"></span>
                                <span class="server-addr" x-text="server.address"></span>
                                <span class="server-time" x-text="server.propagated ? server.found_after : '-'"></span>
                                <span class="server-record" x-text="server.record || '-'" :title="server.record"></span>
                            </div>
                        </template>
                    </div>
                </div>

                <div class="meta">
                    Checked at: <span x-text="result.checked_at"></span>
                </div>
            </div>
        </template>
    </div>

    <script>
        function dnsChecker() {
            return {
                domain: '',
                recordType: 'a',
                match: '',
                timeout: '1m',
                retry: '5s',
                loading: false,
                error: null,
                result: null,
                eventSource: null,

                runCheck() {
                    this.loading = true;
                    this.error = null;
                    this.result = {
                        domain: this.domain,
                        record_type: this.recordType.toUpperCase(),
                        match: this.match,
                        authoritative: [],
                        resolvers: [],
                        all_propagated: false,
                        checked_at: new Date().toISOString()
                    };

                    const params = new URLSearchParams({
                        domain: this.domain,
                        type: this.recordType,
                        match: this.match,
                        timeout: this.timeout,
                        retry: this.retry
                    });

                    this.eventSource = new EventSource('/check/stream?' + params);

                    this.eventSource.onmessage = (event) => {
                        const data = JSON.parse(event.data);

                        switch (data.type) {
                            case 'discovered':
                                this.result.authoritative.push(data.server);
                                break;
                            case 'resolver':
                                this.result.resolvers.push(data.server);
                                break;
                            case 'auth_propagated':
                                const authIdx = this.result.authoritative.findIndex(s => s.name === data.server.name);
                                if (authIdx !== -1) {
                                    this.result.authoritative[authIdx] = data.server;
                                }
                                break;
                            case 'resolver_propagated':
                                const resIdx = this.result.resolvers.findIndex(s => s.name === data.server.name);
                                if (resIdx !== -1) {
                                    this.result.resolvers[resIdx] = data.server;
                                }
                                break;
                            case 'complete':
                                this.result.all_propagated = true;
                                this.result.checked_at = new Date().toISOString();
                                this.loading = false;
                                this.eventSource.close();
                                this.eventSource = null;
                                break;
                            case 'timeout':
                                this.result.checked_at = new Date().toISOString();
                                this.loading = false;
                                this.eventSource.close();
                                this.eventSource = null;
                                break;
                            case 'error':
                                this.error = data.error;
                                this.loading = false;
                                this.eventSource.close();
                                this.eventSource = null;
                                break;
                        }
                    };

                    this.eventSource.onerror = () => {
                        if (this.loading) {
                            this.error = 'Connection lost';
                            this.loading = false;
                        }
                        if (this.eventSource) {
                            this.eventSource.close();
                            this.eventSource = null;
                        }
                    };
                },

                cancelCheck() {
                    if (this.eventSource) {
                        this.eventSource.close();
                        this.eventSource = null;
                        this.loading = false;
                        this.error = 'Check cancelled';
                    }
                }
            }
        }
    </script>
</body>
</html>`

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var domain, recordType, match string
	var timeout, retry time.Duration

	if r.Method == http.MethodPost {
		var req struct {
			Domain  string `json:"domain"`
			Type    string `json:"type"`
			Match   string `json:"match"`
			Timeout string `json:"timeout"`
			Retry   string `json:"retry"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: "invalid JSON body"})
			return
		}
		domain = req.Domain
		recordType = req.Type
		match = req.Match

		if req.Timeout != "" {
			var err error
			timeout, err = time.ParseDuration(req.Timeout)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: "invalid timeout format"})
				return
			}
		}
		if req.Retry != "" {
			var err error
			retry, err = time.ParseDuration(req.Retry)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: "invalid retry format"})
				return
			}
		}
	} else {
		domain = r.URL.Query().Get("domain")
		recordType = r.URL.Query().Get("type")
		match = r.URL.Query().Get("match")

		if t := r.URL.Query().Get("timeout"); t != "" {
			var err error
			timeout, err = time.ParseDuration(t)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: "invalid timeout format"})
				return
			}
		}
		if rt := r.URL.Query().Get("retry"); rt != "" {
			var err error
			retry, err = time.ParseDuration(rt)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: "invalid retry format"})
				return
			}
		}
	}

	// Defaults
	if recordType == "" {
		recordType = "a"
	}
	if timeout == 0 {
		timeout = 1 * time.Minute
	}
	if retry == 0 {
		retry = 5 * time.Second
	}

	// Validation
	if domain == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: "domain is required"})
		return
	}
	if match == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: "match is required"})
		return
	}

	dnsType := dnspkg.ParseRecordType(recordType)
	if dnsType == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: fmt.Sprintf("unsupported record type: %s", recordType)})
		return
	}

	// Ensure domain is FQDN
	if !strings.HasSuffix(domain, ".") {
		domain = domain + "."
	}

	// Run the check
	response, err := dnspkg.CheckPropagation(&config, domain, recordType, match, dnsType, timeout, retry)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(dnspkg.ErrorResponse{Error: err.Error()})
		return
	}

	json.NewEncoder(w).Encode(response)
}

// SSE event types
type StreamEvent struct {
	Type   string            `json:"type"`
	Server dnspkg.ServerStatus `json:"server,omitempty"`
	Error  string            `json:"error,omitempty"`
}

func handleCheckStream(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Parse parameters
	domain := r.URL.Query().Get("domain")
	recordType := r.URL.Query().Get("type")
	match := r.URL.Query().Get("match")

	var timeout, retry time.Duration
	if t := r.URL.Query().Get("timeout"); t != "" {
		timeout, _ = time.ParseDuration(t)
	}
	if rt := r.URL.Query().Get("retry"); rt != "" {
		retry, _ = time.ParseDuration(rt)
	}

	// Defaults
	if recordType == "" {
		recordType = "a"
	}
	if timeout == 0 {
		timeout = 1 * time.Minute
	}
	if retry == 0 {
		retry = 5 * time.Second
	}

	// Validation
	if domain == "" {
		sendSSE(w, flusher, StreamEvent{Type: "error", Error: "domain is required"})
		return
	}
	if match == "" {
		sendSSE(w, flusher, StreamEvent{Type: "error", Error: "match is required"})
		return
	}

	dnsType := dnspkg.ParseRecordType(recordType)
	if dnsType == 0 {
		sendSSE(w, flusher, StreamEvent{Type: "error", Error: "unsupported record type"})
		return
	}

	// Ensure domain is FQDN
	if !strings.HasSuffix(domain, ".") {
		domain = domain + "."
	}

	// Run streaming check
	runCheckStream(r.Context(), w, flusher, domain, recordType, match, dnsType, timeout, retry)
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, event StreamEvent) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func runCheckStream(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, domain, recordType, match string, dnsType uint16, timeout, retry time.Duration) {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Find authoritative nameservers
	authServers, err := dnspkg.FindAuthoritativeServers(domain, config.RootServers)
	if err != nil {
		sendSSE(w, flusher, StreamEvent{Type: "error", Error: fmt.Sprintf("failed to find authoritative servers: %v", err)})
		return
	}

	// Send discovered authoritative servers
	for _, s := range authServers {
		sendSSE(w, flusher, StreamEvent{
			Type: "discovered",
			Server: dnspkg.ServerStatus{
				Name:       s.Name,
				Address:    strings.TrimSuffix(s.Addr, ":53"),
				Propagated: false,
			},
		})
	}

	// Build public resolver list
	resolvers := make([]*dnspkg.ResolverStatus, 0, len(config.PublicResolvers)+1)
	for _, addr := range config.PublicResolvers {
		resolvers = append(resolvers, &dnspkg.ResolverStatus{
			Name: strings.Split(addr, ":")[0],
			Addr: addr,
		})
	}
	resolvers = append(resolvers, &dnspkg.ResolverStatus{
		Name: "local",
		Addr: "",
	})

	// Send resolver list
	for _, r := range resolvers {
		addr := r.Addr
		if addr == "" {
			addr = "system"
		} else {
			addr = strings.TrimSuffix(addr, ":53")
		}
		sendSSE(w, flusher, StreamEvent{
			Type: "resolver",
			Server: dnspkg.ServerStatus{
				Name:       r.Name,
				Address:    addr,
				Propagated: false,
			},
		})
	}

	var mu sync.Mutex
	startTime := time.Now()

	// Channel to receive propagation events
	eventCh := make(chan StreamEvent, 100)

	// Check with retries until timeout or all done
	ticker := time.NewTicker(retry)
	defer ticker.Stop()

	checkAll := func() {
		// Check authoritative servers
		var wg sync.WaitGroup
		for _, s := range authServers {
			mu.Lock()
			if s.Propagated {
				mu.Unlock()
				continue
			}
			mu.Unlock()

			wg.Add(1)
			go func(s *dnspkg.ResolverStatus) {
				defer wg.Done()
				record := dnspkg.QueryAuthoritativeRecord(s.Addr, domain, dnsType, match)
				if record != "" {
					mu.Lock()
					if !s.Propagated {
						s.Propagated = true
						s.FoundAt = time.Since(startTime)
						s.Record = record
						eventCh <- StreamEvent{
							Type: "auth_propagated",
							Server: dnspkg.ServerStatus{
								Name:       s.Name,
								Address:    strings.TrimSuffix(s.Addr, ":53"),
								Propagated: true,
								FoundAfter: dnspkg.FormatDuration(s.FoundAt),
								Record:     record,
							},
						}
					}
					mu.Unlock()
				}
			}(s)
		}
		wg.Wait()

		// Check resolvers
		for _, r := range resolvers {
			mu.Lock()
			if r.Propagated {
				mu.Unlock()
				continue
			}
			mu.Unlock()

			wg.Add(1)
			go func(r *dnspkg.ResolverStatus) {
				defer wg.Done()
				record, found := dnspkg.CheckResolver(r.Addr, strings.TrimSuffix(domain, "."), recordType, match)
				if found {
					mu.Lock()
					if !r.Propagated {
						r.Propagated = true
						r.FoundAt = time.Since(startTime)
						r.Record = record
						addr := r.Addr
						if addr == "" {
							addr = "system"
						} else {
							addr = strings.TrimSuffix(addr, ":53")
						}
						eventCh <- StreamEvent{
							Type: "resolver_propagated",
							Server: dnspkg.ServerStatus{
								Name:       r.Name,
								Address:    addr,
								Propagated: true,
								FoundAfter: dnspkg.FormatDuration(r.FoundAt),
								Record:     record,
							},
						}
					}
					mu.Unlock()
				}
			}(r)
		}
		wg.Wait()
	}

	// Goroutine to send events
	done := make(chan struct{})
	go func() {
		for {
			select {
			case event := <-eventCh:
				sendSSE(w, flusher, event)
			case <-done:
				// Drain remaining events
				for {
					select {
					case event := <-eventCh:
						sendSSE(w, flusher, event)
					default:
						return
					}
				}
			}
		}
	}()

	// Initial check
	checkAll()

	for {
		// Check if all done
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
			close(done)
			time.Sleep(50 * time.Millisecond) // Allow pending events to drain
			sendSSE(w, flusher, StreamEvent{Type: "complete"})
			return
		}

		select {
		case <-ctx.Done():
			close(done)
			time.Sleep(50 * time.Millisecond) // Allow pending events to drain
			sendSSE(w, flusher, StreamEvent{Type: "timeout"})
			return
		case <-ticker.C:
			checkAll()
		}
	}
}

func runCLI(domain, recordType, match string, retryInterval, duration time.Duration) {
	// Ensure domain is FQDN
	if !strings.HasSuffix(domain, ".") {
		domain = domain + "."
	}

	dnsType := dnspkg.ParseRecordType(recordType)
	if dnsType == 0 {
		fmt.Fprintf(os.Stderr, "Error: unsupported record type %q\n", recordType)
		os.Exit(1)
	}

	fmt.Printf("Testing DNS propagation for %s (%s=%s)\n", strings.TrimSuffix(domain, "."), strings.ToUpper(recordType), match)
	fmt.Printf("Retry interval: %s, Max duration: %s\n\n", retryInterval, duration)

	// Step 1: Find all authoritative nameservers
	fmt.Println("=== Discovering authoritative nameservers ===")
	authServers, err := dnspkg.FindAuthoritativeServers(domain, config.RootServers)
	if err != nil {
		fmt.Printf("Error finding authoritative servers: %v\n", err)
		os.Exit(1)
	}

	if len(authServers) == 0 {
		fmt.Println("No authoritative nameservers found")
		os.Exit(1)
	}

	fmt.Printf("Found %d authoritative nameservers:\n", len(authServers))
	for _, ns := range authServers {
		fmt.Printf("  - %s (%s)\n", ns.Name, strings.TrimSuffix(ns.Addr, ":53"))
	}
	fmt.Println()

	// Step 2: Check all authoritative nameservers
	fmt.Println("=== Checking authoritative nameservers ===")
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var mu sync.Mutex
	allAuthPropagated := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range authServers {
			if !r.Propagated {
				return false
			}
		}
		return true
	}

	// Initial check
	dnspkg.CheckAuthoritativeAllVerbose(authServers, domain, dnsType, match, recordType, startTime, &mu)

	if !allAuthPropagated() {
		ticker := time.NewTicker(retryInterval)
		for !allAuthPropagated() {
			select {
			case <-ctx.Done():
				fmt.Printf("\nTimeout: not all authoritative servers have the record\n")
				dnspkg.PrintSummary(authServers, "authoritative")
				ticker.Stop()
				os.Exit(1)
			case <-ticker.C:
				dnspkg.CheckAuthoritativeAllVerbose(authServers, domain, dnsType, match, recordType, startTime, &mu)
			}
		}
		ticker.Stop()
	}

	fmt.Println("\nAll authoritative nameservers have the record!")

	// Step 3: Check public resolvers
	fmt.Println("\n=== Checking public resolvers for propagation ===")
	resolvers := make([]*dnspkg.ResolverStatus, 0, len(config.PublicResolvers)+1)
	for _, addr := range config.PublicResolvers {
		resolvers = append(resolvers, &dnspkg.ResolverStatus{
			Name: strings.Split(addr, ":")[0],
			Addr: addr,
		})
	}
	resolvers = append(resolvers, &dnspkg.ResolverStatus{
		Name: "local",
		Addr: "", // empty means use system resolver
	})

	resolverStartTime := time.Now()
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	allResolversPropagated := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range resolvers {
			if !r.Propagated {
				return false
			}
		}
		return true
	}

	// Initial check
	dnspkg.CheckResolverAllVerbose(resolvers, domain, recordType, match, resolverStartTime, &mu)

	for !allResolversPropagated() {
		select {
		case <-ctx.Done():
			fmt.Printf("\nTimeout reached after %s\n", duration)
			dnspkg.PrintSummary(resolvers, "resolver")
			os.Exit(1)
		case <-ticker.C:
			dnspkg.CheckResolverAllVerbose(resolvers, domain, recordType, match, resolverStartTime, &mu)
		}
	}

	fmt.Printf("\nAll resolvers propagated!\n")
}
