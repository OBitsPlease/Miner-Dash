package server

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	scanMaxHosts        = 1024
	scanWorkers         = 64
	scanConnectTimeout  = 800 * time.Millisecond
	scanHTTPTimeout     = 2500 * time.Millisecond
	scanAPITimeout      = 1500 * time.Millisecond
	scanHostTimeout     = 15 * time.Second
	scanMaxBodyBytes    = 16 << 10
	scanMaxBannerBytes  = 4096
	scanUserAgent       = "MinerDash-FindMyMiner/1.0"
	scanMinerAPIRequest = `{"command":"version"}`
)

var (
	scanPorts    = []int{22, 80, 443, 1337, 4028, 4200, 8080, 8443, 8888}
	scanWebPorts = []int{80, 443, 1337, 4200, 8080, 8443, 8888}
	scanTLSPorts = map[int]bool{443: true, 8443: true}
	// scanURLPreference orders the management links offered for a device.
	scanURLPreference = []int{80, 443, 8080, 8443, 8888, 1337, 4200}
)

type scanNetwork struct {
	Name        string `json:"name"`
	CIDR        string `json:"cidr"`
	Address     string `json:"address"`
	Hosts       int    `json:"hosts"`
	Recommended bool   `json:"recommended"`
}

type scanDevice struct {
	IP            string    `json:"ip"`
	Hostname      string    `json:"hostname"`
	MAC           string    `json:"mac"`
	OpenPorts     []int     `json:"open_ports"`
	Platform      string    `json:"platform"`
	Model         string    `json:"model"`
	Score         int       `json:"score"`
	Confidence    string    `json:"confidence"`
	Reasons       []string  `json:"reasons"`
	ManagementURL string    `json:"management_url"`
	ResponseMS    int64     `json:"response_ms"`
	IsMiner       bool      `json:"is_miner"`
	EnrolledRigID string    `json:"enrolled_rig_id"`
	LastSeen      time.Time `json:"last_seen"`
}

type scanStatus struct {
	Running    bool         `json:"running"`
	CIDR       string       `json:"cidr"`
	Scanned    int          `json:"scanned"`
	Total      int          `json:"total"`
	StartedAt  *time.Time   `json:"started_at"`
	FinishedAt *time.Time   `json:"finished_at"`
	Error      string       `json:"error"`
	Devices    []scanDevice `json:"devices"`
}

type networkScanner struct {
	mu         sync.Mutex
	running    bool
	cidr       string
	scanned    int
	total      int
	startedAt  time.Time
	finishedAt time.Time
	scanError  string
	devices    []scanDevice
	cancel     context.CancelFunc
	client     *http.Client
}

func newNetworkScanner() *networkScanner {
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		DisableKeepAlives: true,
	}
	return &networkScanner{
		devices: []scanDevice{},
		client: &http.Client{
			Transport: transport,
			Timeout:   scanHTTPTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (s *networkScanner) status() scanStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := scanStatus{
		Running: s.running, CIDR: s.cidr, Scanned: s.scanned, Total: s.total,
		Error: s.scanError, Devices: append([]scanDevice{}, s.devices...),
	}
	if !s.startedAt.IsZero() {
		started := s.startedAt
		status.StartedAt = &started
	}
	if !s.finishedAt.IsZero() {
		finished := s.finishedAt
		status.FinishedAt = &finished
	}
	return status
}

func (s *networkScanner) stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *networkScanner) start(cidr string) error {
	hosts, network, err := enumerateHosts(cidr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("a scan is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.running = true
	s.cancel = cancel
	s.cidr = network
	s.scanned = 0
	s.total = len(hosts)
	s.startedAt = time.Now().UTC()
	s.finishedAt = time.Time{}
	s.scanError = ""
	s.devices = []scanDevice{}
	s.mu.Unlock()
	go s.run(ctx, hosts)
	return nil
}

func (s *networkScanner) run(ctx context.Context, hosts []string) {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.finishedAt = time.Now().UTC()
		s.cancel = nil
		s.mu.Unlock()
	}()
	arp := arpTable()
	queue := make(chan string)
	var waitGroup sync.WaitGroup
	for worker := 0; worker < scanWorkers; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for address := range queue {
				hostCtx, cancel := context.WithTimeout(ctx, scanHostTimeout)
				device, found := s.probeHost(hostCtx, address, arp[address])
				cancel()
				s.mu.Lock()
				s.scanned++
				if found {
					s.devices = append(s.devices, device)
					sortScanDevices(s.devices)
				}
				s.mu.Unlock()
			}
		}()
	}
	for _, address := range hosts {
		select {
		case queue <- address:
		case <-ctx.Done():
			close(queue)
			waitGroup.Wait()
			s.mu.Lock()
			s.scanError = "scan cancelled"
			s.mu.Unlock()
			return
		}
	}
	close(queue)
	waitGroup.Wait()
}

func (s *networkScanner) probeHost(ctx context.Context, address, mac string) (scanDevice, bool) {
	start := time.Now()
	openPorts := make([]int, 0, len(scanPorts))
	var portMu sync.Mutex
	var waitGroup sync.WaitGroup
	for _, port := range scanPorts {
		waitGroup.Add(1)
		go func(port int) {
			defer waitGroup.Done()
			if tcpOpen(ctx, address, port) {
				portMu.Lock()
				openPorts = append(openPorts, port)
				portMu.Unlock()
			}
		}(port)
	}
	waitGroup.Wait()
	sort.Ints(openPorts)
	if len(openPorts) == 0 && mac == "" {
		return scanDevice{}, false
	}
	device := scanDevice{
		IP: address, MAC: mac, OpenPorts: openPorts,
		ResponseMS: time.Since(start).Milliseconds(), LastSeen: time.Now().UTC(),
		Reasons: []string{}, Confidence: "unknown",
	}
	pages := make(map[int]scanPage)
	banner := ""
	var pageMu sync.Mutex
	for _, port := range openPorts {
		if port == 4028 {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				text := minerAPIBanner(ctx, address)
				pageMu.Lock()
				banner = text
				pageMu.Unlock()
			}()
			continue
		}
		if !containsInt(scanWebPorts, port) {
			continue
		}
		waitGroup.Add(1)
		go func(port int) {
			defer waitGroup.Done()
			page, ok := s.httpProbe(ctx, address, port)
			if !ok {
				return
			}
			pageMu.Lock()
			pages[port] = page
			pageMu.Unlock()
		}(port)
	}
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		device.Hostname = reverseLookup(ctx, address)
	}()
	waitGroup.Wait()
	detection := fingerprintDevice(device.Hostname, openPorts, pages, banner)
	device.Platform = detection.Platform
	device.Model = detection.Model
	device.Score = detection.Score
	device.Confidence = detection.Confidence
	device.Reasons = detection.Reasons
	device.IsMiner = detection.Score >= 30
	device.ManagementURL = chooseManagementURL(address, openPorts, pages, detection.MatchedPort)
	return device, true
}

type scanPage struct {
	Port   int
	Scheme string
	Body   string
	Title  string
}

func (s *networkScanner) httpProbe(ctx context.Context, address string, port int) (scanPage, bool) {
	schemes := []string{"http", "https"}
	if scanTLSPorts[port] {
		schemes = []string{"https", "http"}
	}
	for _, scheme := range schemes {
		endpoint := fmt.Sprintf("%s://%s:%d/", scheme, address, port)
		requestCtx, cancel := context.WithTimeout(ctx, scanHTTPTimeout)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			continue
		}
		request.Header.Set("User-Agent", scanUserAgent)
		response, err := s.client.Do(request)
		if err != nil {
			cancel()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, scanMaxBodyBytes))
		response.Body.Close()
		cancel()
		page := scanPage{Port: port, Scheme: scheme, Body: strings.ToLower(string(body))}
		page.Title = extractHTMLTitle(string(body))
		return page, true
	}
	return scanPage{}, false
}

func minerAPIBanner(ctx context.Context, address string) string {
	dialer := net.Dialer{Timeout: scanAPITimeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, "4028"))
	if err != nil {
		return ""
	}
	defer connection.Close()
	deadline := time.Now().Add(scanAPITimeout)
	_ = connection.SetDeadline(deadline)
	if _, err := connection.Write([]byte(scanMinerAPIRequest)); err != nil {
		return ""
	}
	buffer := make([]byte, scanMaxBannerBytes)
	read, err := connection.Read(buffer)
	if err != nil && read == 0 {
		return ""
	}
	text := string(buffer[:read])
	if cut := strings.IndexByte(text, 0); cut >= 0 {
		text = text[:cut]
	}
	text = strings.TrimSpace(text)
	if len(text) > 1024 {
		text = text[:1024]
	}
	return strings.ToLower(text)
}

func tcpOpen(ctx context.Context, address string, port int) bool {
	dialer := net.Dialer{Timeout: scanConnectTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func reverseLookup(ctx context.Context, address string) string {
	lookupCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(lookupCtx, address)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSpace(names[0]), ".")
}

var htmlTitlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func extractHTMLTitle(body string) string {
	match := htmlTitlePattern.FindStringSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	return cleanHTMLText(match[1])
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func chooseManagementURL(address string, openPorts []int, pages map[int]scanPage, matchedPort int) string {
	if matchedPort > 0 {
		if page, ok := pages[matchedPort]; ok {
			return managementURL(address, page)
		}
	}
	for _, port := range scanURLPreference {
		if page, ok := pages[port]; ok {
			return managementURL(address, page)
		}
	}
	if containsInt(openPorts, 4200) {
		return fmt.Sprintf("http://%s:4200/", address)
	}
	return ""
}

func managementURL(address string, page scanPage) string {
	if (page.Scheme == "http" && page.Port == 80) || (page.Scheme == "https" && page.Port == 443) {
		return fmt.Sprintf("%s://%s/", page.Scheme, address)
	}
	return fmt.Sprintf("%s://%s:%d/", page.Scheme, address, page.Port)
}

func sortScanDevices(devices []scanDevice) {
	sort.SliceStable(devices, func(i, j int) bool {
		if devices[i].Score != devices[j].Score {
			return devices[i].Score > devices[j].Score
		}
		return ipSortKey(devices[i].IP) < ipSortKey(devices[j].IP)
	})
}

func ipSortKey(address string) uint32 {
	parsed := net.ParseIP(address).To4()
	if parsed == nil {
		return 0
	}
	return binary.BigEndian.Uint32(parsed)
}

// enumerateHosts expands a CIDR into scannable host addresses, refusing ranges
// large enough to turn a discovery scan into a network event.
func enumerateHosts(cidr string) ([]string, string, error) {
	address, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, "", fmt.Errorf("enter a valid network such as 192.168.1.0/24")
	}
	if address.To4() == nil {
		return nil, "", fmt.Errorf("only IPv4 networks can be scanned")
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return nil, "", fmt.Errorf("only IPv4 networks can be scanned")
	}
	count := 1 << uint(bits-ones)
	if count > scanMaxHosts+2 {
		return nil, "", fmt.Errorf("network %s has too many addresses; scan a /22 or smaller", network.String())
	}
	base := binary.BigEndian.Uint32(network.IP.To4())
	hosts := make([]string, 0, count)
	for offset := 0; offset < count; offset++ {
		if count > 2 && (offset == 0 || offset == count-1) {
			continue
		}
		host := make(net.IP, 4)
		binary.BigEndian.PutUint32(host, base+uint32(offset))
		hosts = append(hosts, host.String())
	}
	return hosts, network.String(), nil
}

// localScanNetworks lists the private IPv4 networks this controller can reach.
func localScanNetworks() []scanNetwork {
	interfaces, err := net.Interfaces()
	if err != nil {
		return []scanNetwork{}
	}
	networks := make([]scanNetwork, 0, 4)
	for _, device := range interfaces {
		if device.Flags&net.FlagUp == 0 || device.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := device.Addrs()
		if err != nil {
			continue
		}
		for _, entry := range addresses {
			prefix, ok := entry.(*net.IPNet)
			if !ok || prefix.IP.To4() == nil || !prefix.IP.IsPrivate() {
				continue
			}
			ones, bits := prefix.Mask.Size()
			if bits != 32 {
				continue
			}
			cidr := (&net.IPNet{IP: prefix.IP.Mask(prefix.Mask), Mask: prefix.Mask}).String()
			hosts := 1<<uint(bits-ones) - 2
			if hosts > scanMaxHosts {
				cidr = fmt.Sprintf("%s/24", prefix.IP.Mask(net.CIDRMask(24, 32)).String())
				hosts = 254
			}
			networks = append(networks, scanNetwork{
				Name: device.Name, CIDR: cidr, Address: prefix.IP.String(), Hosts: hosts,
			})
		}
	}
	networks = dedupeScanNetworks(networks)
	sort.SliceStable(networks, func(i, j int) bool { return networks[i].Name < networks[j].Name })
	if len(networks) > 0 {
		networks[0].Recommended = true
	}
	return networks
}

// dedupeScanNetworks keeps one entry per subnet so adapters that hold several
// addresses on the same network do not produce duplicate choices.
func dedupeScanNetworks(networks []scanNetwork) []scanNetwork {
	seen := make(map[string]bool, len(networks))
	unique := make([]scanNetwork, 0, len(networks))
	for _, network := range networks {
		if seen[network.CIDR] {
			continue
		}
		seen[network.CIDR] = true
		unique = append(unique, network)
	}
	return unique
}

var arpEntryPattern = regexp.MustCompile(`(\d{1,3}(?:\.\d{1,3}){3})\s+([0-9a-fA-F]{2}(?:[:-][0-9a-fA-F]{2}){5})`)

// arpTable reads the operating system neighbour cache, which reveals hosts that
// refuse every probed port.
func arpTable() map[string]string {
	table := make(map[string]string)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "arp", "-a").CombinedOutput()
	if err != nil && len(output) == 0 {
		return table
	}
	for _, match := range arpEntryPattern.FindAllStringSubmatch(string(output), -1) {
		mac := strings.ToUpper(strings.ReplaceAll(match[2], "-", ":"))
		if mac == "FF:FF:FF:FF:FF:FF" || strings.HasPrefix(mac, "01:00:5E") {
			continue
		}
		table[match[1]] = mac
	}
	return table
}
