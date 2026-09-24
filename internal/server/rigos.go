package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

const rigOSImageFilename = "minerdash-rig-os.img.xz"

var minerCatalog = []protocol.MinerCatalogEntry{
	{ID: "rigel", Name: "Rigel", Description: "NVIDIA-focused multi-algorithm GPU miner.", Hardware: []string{"NVIDIA"}, License: "Proprietary", Redistribution: "Downloaded from the official release repository for use on your rigs; review the vendor license.", SourceURL: "https://github.com/rigelminer/rigel/releases", BinaryName: "rigel", DefaultArguments: []string{"-o", "{POOL}", "-u", "{WALLET}.{WORKER}", "-p", "{PASSWORD}"}, AlgorithmFlag: "-a", PackageRequired: true, AutomaticInstall: true},
	{ID: "wildrig-multi", Name: "WildRig Multi", Description: "Multi-algorithm AMD, NVIDIA, and Intel GPU miner.", Hardware: []string{"AMD", "NVIDIA", "Intel GPU"}, License: "Proprietary", Redistribution: "Downloaded from the official release repository for use on your rigs; review the vendor license.", SourceURL: "https://github.com/andru-kun/wildrig-multi/releases", BinaryName: "wildrig-multi", DefaultArguments: []string{"--url", "{POOL}", "--user", "{WALLET}.{WORKER}", "--pass", "{PASSWORD}"}, AlgorithmFlag: "--algo", PackageRequired: true, AutomaticInstall: true},
	{ID: "srbminer-multi", Name: "SRBMiner-MULTI", Description: "CPU and AMD/NVIDIA GPU multi-algorithm miner.", Hardware: []string{"CPU", "AMD", "NVIDIA"}, License: "Proprietary", Redistribution: "Downloaded from the official release repository for use on your rigs; review the vendor license.", SourceURL: "https://github.com/doktor83/SRBMiner-Multi/releases", BinaryName: "SRBMiner-MULTI", DefaultArguments: []string{"--pool", "{POOL}", "--wallet", "{WALLET}", "--password", "{PASSWORD}", "--worker", "{WORKER}"}, AlgorithmFlag: "--algorithm", PackageRequired: true, AutomaticInstall: true},
	{ID: "miniz", Name: "miniZ", Description: "NVIDIA-focused Equihash-family GPU miner with local telemetry.", Hardware: []string{"NVIDIA"}, License: "Proprietary", Redistribution: "Downloaded directly from the official GitHub release for private use; review the vendor terms.", SourceURL: "https://github.com/miniZ-miner/miniZ/releases", BinaryName: "miniZ", DefaultArguments: []string{"--url={WALLET}.{WORKER}@{POOL_ENDPOINT}", "--pass={PASSWORD}", "--telemetry=127.0.0.1:20000", "--gpu-line"}, AlgorithmFlag: "--par", PackageRequired: true, AutomaticInstall: true},
	{ID: "xmrig", Name: "XMRig", Description: "Open-source CPU and AMD/NVIDIA RandomX miner.", Hardware: []string{"CPU", "AMD", "NVIDIA"}, License: "GPL-3.0", Redistribution: "The signed Ubuntu package is included in Rig OS; GPL source remains available from Ubuntu.", SourceURL: "https://github.com/xmrig/xmrig", BinaryName: "xmrig", DefaultArguments: []string{"-o", "{POOL}", "-u", "{WALLET}", "-p", "{PASSWORD}", "--rig-id", "{WORKER}", "--http-host", "127.0.0.1", "--http-port", "18080"}, AlgorithmFlag: "-a", ImageIncluded: true},
	{ID: "lolminer", Name: "lolMiner", Description: "AMD and NVIDIA multi-algorithm GPU miner.", Hardware: []string{"AMD", "NVIDIA"}, License: "Proprietary", Redistribution: "Downloaded directly from the official GitHub release for private use; review the bundled license.", SourceURL: "https://github.com/Lolliedieb/lolMiner-releases/releases", BinaryName: "lolMiner", DefaultArguments: []string{"--pool", "{POOL}", "--user", "{WALLET}.{WORKER}", "--pass", "{PASSWORD}"}, AlgorithmFlag: "--algo", PackageRequired: true, AutomaticInstall: true},
	{ID: "teamredminer", Name: "TeamRedMiner", Description: "AMD GPU optimized multi-algorithm miner.", Hardware: []string{"AMD"}, License: "Proprietary", Redistribution: "Operator must obtain the binary and written redistribution permission.", SourceURL: "https://github.com/todxx/teamredminer", BinaryName: "teamredminer", DefaultArguments: []string{"-o", "{POOL}", "-u", "{WALLET}.{WORKER}", "-p", "{PASSWORD}"}, AlgorithmFlag: "-a", PackageRequired: true},
	{ID: "bzminer", Name: "BzMiner", Description: "AMD, NVIDIA, and Intel GPU multi-algorithm miner.", Hardware: []string{"AMD", "NVIDIA", "Intel GPU"}, License: "Proprietary", Redistribution: "Downloaded directly from the official GitHub release for private use; review the vendor terms.", SourceURL: "https://github.com/bzminer/bzminer/releases", BinaryName: "bzminer", DefaultArguments: []string{"-p", "{POOL}", "-w", "{WALLET}", "--worker_name", "{WORKER}"}, AlgorithmFlag: "-a", PackageRequired: true, AutomaticInstall: true},
	{ID: "gminer", Name: "GMiner", Description: "AMD and NVIDIA multi-algorithm GPU miner.", Hardware: []string{"AMD", "NVIDIA"}, License: "Proprietary", Redistribution: "Operator must obtain the binary and any required redistribution permission.", SourceURL: "https://github.com/develsoftware/GMinerRelease/releases", BinaryName: "miner", DefaultArguments: []string{"--server", "{POOL}", "--user", "{WALLET}.{WORKER}", "--pass", "{PASSWORD}"}, AlgorithmFlag: "--algo", PackageRequired: true},
	{ID: "trex", Name: "T-Rex", Description: "NVIDIA multi-algorithm GPU miner.", Hardware: []string{"NVIDIA"}, License: "Proprietary", Redistribution: "Operator must obtain the binary and any required redistribution permission.", SourceURL: "https://github.com/trexminer/T-Rex/releases", BinaryName: "t-rex", DefaultArguments: []string{"-o", "{POOL}", "-u", "{WALLET}.{WORKER}", "-p", "{PASSWORD}"}, AlgorithmFlag: "-a", PackageRequired: true},
	{ID: "ethminer", Name: "Ethminer", Description: "Archived Ethereum GPU miner; the latest Linux builds target legacy CUDA 8/9.", Hardware: []string{"AMD", "NVIDIA"}, License: "GPL-3.0", Redistribution: "Upstream is archived and its 2019 binaries may not support current drivers. Manual compatibility review is required.", SourceURL: "https://github.com/ethereum-mining/ethminer/releases", BinaryName: "ethminer", DefaultArguments: []string{"-P", "{POOL}"}, AlgorithmFlag: "-A", PackageRequired: true},
	{ID: "hellminer", Name: "Hellminer", Description: "VerusHash CPU miner maintained as source files without official release packages.", Hardware: []string{"CPU"}, License: "Unspecified", Redistribution: "No official GitHub release archive or redistribution grant is available. Manual reviewed package only.", SourceURL: "https://github.com/vrscms/hellminer", BinaryName: "hellminer", DefaultArguments: []string{"-c", "{POOL}", "-u", "{WALLET}.{WORKER}", "-p", "{PASSWORD}"}, AlgorithmFlag: "", PackageRequired: true},
	{ID: "onezerominer", Name: "OneZeroMiner", Description: "Optimized AMD and NVIDIA multi-algorithm GPU miner.", Hardware: []string{"AMD", "NVIDIA"}, License: "Proprietary", Redistribution: "Downloaded directly from the official GitHub release for private use; review the vendor terms.", SourceURL: "https://github.com/OneZeroMiner/onezerominer/releases", BinaryName: "onezerominer", DefaultArguments: []string{"--pool", "{POOL}", "--wallet", "{WALLET}", "--worker", "{WORKER}", "--password", "{PASSWORD}"}, AlgorithmFlag: "--algo", PackageRequired: true, AutomaticInstall: true},
	{ID: "qubminer", Name: "QubMiner", Description: "Legacy Qubic.li CPU and NVIDIA miner package.", Hardware: []string{"CPU", "NVIDIA"}, License: "GPL-3.0 repository", Redistribution: "The latest requested release is marked final and bundles external components. Manual review is required before installation.", SourceURL: "https://github.com/Worm/qubminer/releases", BinaryName: "qli-Client", DefaultArguments: []string{}, AlgorithmFlag: "", PackageRequired: true},
	{ID: "cpuminer-opt", Name: "CPUMiner-OPT", Description: "Open-source multi-algorithm optimized CPU miner.", Hardware: []string{"CPU"}, License: "GPL-2.0", Redistribution: "The current official release provides Windows binaries only. A reviewed Linux build must be supplied manually.", SourceURL: "https://github.com/JayDDee/cpuminer-opt/releases", BinaryName: "cpuminer", DefaultArguments: []string{"-o", "{POOL}", "-u", "{WALLET}.{WORKER}", "-p", "{PASSWORD}"}, AlgorithmFlag: "-a", PackageRequired: true},
	{ID: "bc3hashminer", Name: "BCIII Hash Miner", Description: "GPU miner for BC3 requiring an operator-supplied reviewed package.", Hardware: []string{"GPU"}, License: "Unknown", Redistribution: "No verified public Linux release source is configured. Manual reviewed package only.", SourceURL: "https://github.com/OBitsPlease/MinerDash-Custom-Miners", BinaryName: "bc3hashminer", DefaultArguments: []string{"-o", "{POOL}", "-u", "{WALLET}.{WORKER}", "-p", "{PASSWORD}"}, AlgorithmFlag: "-a", PackageRequired: true},
}

func catalogEntry(id string) (protocol.MinerCatalogEntry, bool) {
	for _, entry := range minerCatalog {
		if entry.ID == id {
			return entry, true
		}
	}
	return protocol.MinerCatalogEntry{}, false
}

func catalogMinerMatches(entry protocol.MinerCatalogEntry, miner protocol.MinerDefinition) bool {
	if miner.CatalogID != "" {
		return miner.CatalogID == entry.ID
	}
	return strings.EqualFold(strings.TrimSpace(miner.Name), entry.Name) &&
		strings.EqualFold(strings.TrimSpace(miner.BinaryName), entry.BinaryName)
}

func catalogMinerDefinition(entry protocol.MinerCatalogEntry) protocol.MinerDefinition {
	managed := entry.PackageRequired
	profile := ""
	binaryName := ""
	version := "package-required"
	if entry.ImageIncluded {
		profile = entry.ID
		version = "Ubuntu 24.04 package"
	} else {
		binaryName = entry.BinaryName
	}
	return protocol.MinerDefinition{
		CatalogID: entry.ID, Name: entry.Name, Version: version, Profile: profile,
		Algorithm: "multi", ManagedBinary: managed, BinaryName: binaryName,
		SourceURL: entry.SourceURL, DefaultArguments: append([]string(nil), entry.DefaultArguments...),
	}
}

func (s *Store) RigOSStatus() protocol.RigOSStatus {
	status := protocol.RigOSStatus{
		ImageFilename:      rigOSImageFilename,
		SupportsUSBRun:     true,
		SupportsLocalDrive: true,
		Miners:             append([]protocol.MinerCatalogEntry(nil), minerCatalog...),
	}
	miners := s.ListMiners()
	for index := range status.Miners {
		for _, miner := range miners {
			if catalogMinerMatches(status.Miners[index], miner) {
				status.Miners[index].InstalledMinerID = miner.ID
				status.Miners[index].InstalledVersion = miner.Version
				status.Miners[index].PackageReady = (miner.ManagedBinary && miner.BinarySHA256 != "") || (status.Miners[index].ImageIncluded && !miner.ManagedBinary && miner.Profile != "")
				break
			}
		}
	}
	imagePath := filepath.Join(filepath.Dir(s.path), "rig-os", rigOSImageFilename)
	info, err := os.Lstat(imagePath)
	if err != nil || !info.Mode().IsRegular() {
		return status
	}
	digestData, err := os.ReadFile(imagePath + ".sha256")
	if err != nil {
		return status
	}
	fields := strings.Fields(string(digestData))
	if len(fields) == 0 {
		return status
	}
	digest := strings.ToLower(fields[0])
	if len(digest) != 64 || !isHex(digest) {
		return status
	}
	status.ImageAvailable = true
	status.ImageSizeBytes = info.Size()
	status.ImageSHA256 = digest
	status.ImageDownloadURL = "/api/v1/rig-os/image-ticket"
	return status
}

func (s *Store) InstallCatalogMiner(catalogID string) (protocol.MinerDefinition, error) {
	entry, found := catalogEntry(catalogID)
	if !found {
		return protocol.MinerDefinition{}, ErrNotFound
	}
	for _, miner := range s.ListMiners() {
		if catalogMinerMatches(entry, miner) {
			return miner, nil
		}
	}
	return s.SaveMiner(catalogMinerDefinition(entry))
}

func (s *HTTPServer) rigOSStatus(w http.ResponseWriter, r *http.Request) {
	status := s.store.RigOSStatus()
	s.releases.enrich(r.Context(), &status)
	if release, err := s.appUpdates.latest(r.Context(), false); err == nil {
		status.ReleaseVersion = strings.TrimPrefix(release.TagName, "v")
		status.ReleaseURL = release.HTMLURL
		for _, asset := range release.Assets {
			switch asset.Name {
			case rigOSImageFilename:
				status.ReleaseImageURL = asset.URL
			case "SHA256SUMS.json":
				status.ReleaseChecksumURL = asset.URL
			}
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *HTTPServer) usbStatus(w http.ResponseWriter, r *http.Request) {
	status := s.usb.status()
	status.ControllerURLs = controllerURLs(r)
	status.TLSFingerprint = s.store.TLSFingerprint()
	writeJSON(w, http.StatusOK, status)
}

func (s *HTTPServer) downloadRigOSImageFromRelease(w http.ResponseWriter, _ *http.Request) {
	operation, err := s.usb.begin("download", "", func(id string) error {
		_, _, _, err := s.usb.ensureImage(context.Background(), id)
		return err
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, operation)
}

type usbActionRequest struct {
	DeviceID      string `json:"device_id"`
	Confirmation  string `json:"confirmation"`
	ControllerURL string `json:"controller_url"`
	RigName       string `json:"rig_name"`
}

func (s *HTTPServer) flashRigOSUSB(w http.ResponseWriter, r *http.Request) {
	var request usbActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !controllerURLAllowed(request.ControllerURL, controllerURLs(r)) {
		writeError(w, errors.New("select a controller LAN address shown by Miner Dash"))
		return
	}
	operation, err := s.usb.begin("flash", request.DeviceID, func(id string) error {
		return s.usb.flash(id, request.DeviceID, request.Confirmation, request.ControllerURL, request.RigName)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, operation)
}

func (s *HTTPServer) pairRigOSUSB(w http.ResponseWriter, r *http.Request) {
	var request usbActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !controllerURLAllowed(request.ControllerURL, controllerURLs(r)) {
		writeError(w, errors.New("select a controller LAN address shown by Miner Dash"))
		return
	}
	operation, err := s.usb.begin("pair", request.DeviceID, func(id string) error {
		return s.usb.pair(id, request.DeviceID, request.ControllerURL, request.RigName)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, operation)
}

func (s *HTTPServer) restoreRigOSUSB(w http.ResponseWriter, r *http.Request) {
	var request usbActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	operation, err := s.usb.begin("restore", request.DeviceID, func(id string) error {
		return s.usb.restore(id, request.DeviceID, request.Confirmation)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, operation)
}

func controllerURLs(r *http.Request) []string {
	port := "8443"
	if _, requestPort, err := net.SplitHostPort(r.Host); err == nil && requestPort != "" {
		port = requestPort
	}
	seen := make(map[string]bool)
	var urls []string
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return urls
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.To4() == nil {
			continue
		}
		value := "https://" + net.JoinHostPort(ip.String(), port)
		if !seen[value] {
			seen[value] = true
			urls = append(urls, value)
		}
	}
	sort.Strings(urls)
	if preferred := preferredControllerURL(port); preferred != "" {
		for index, value := range urls {
			if value == preferred {
				copy(urls[1:index+1], urls[0:index])
				urls[0] = preferred
				break
			}
		}
	}
	return urls
}

func preferredControllerURL(port string) string {
	connection, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return ""
	}
	defer connection.Close()
	address, ok := connection.LocalAddr().(*net.UDPAddr)
	if !ok || address.IP == nil || address.IP.IsLoopback() || address.IP.IsLinkLocalUnicast() || address.IP.To4() == nil {
		return ""
	}
	return "https://" + net.JoinHostPort(address.IP.String(), port)
}

func controllerURLAllowed(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func (s *HTTPServer) installCatalogMiner(w http.ResponseWriter, r *http.Request) {
	catalogID := r.PathValue("catalogID")
	entry, found := catalogEntry(catalogID)
	if !found {
		writeError(w, ErrNotFound)
		return
	}
	var (
		miner protocol.MinerDefinition
		err   error
	)
	if entry.AutomaticInstall {
		miner, err = s.store.EnsureCatalogMinerPackage(r.Context(), catalogID, true)
	} else {
		miner, err = s.store.InstallCatalogMiner(catalogID)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, miner)
}

func (s *HTTPServer) createRigOSImageTicket(w http.ResponseWriter, _ *http.Request) {
	if !s.store.RigOSStatus().ImageAvailable {
		writeError(w, ErrNotFound)
		return
	}
	ticket, err := randomToken(24)
	if err != nil {
		writeError(w, err)
		return
	}
	s.mu.Lock()
	now := time.Now()
	for value, expiresAt := range s.downloads {
		if !expiresAt.After(now) {
			delete(s.downloads, value)
		}
	}
	s.downloads[ticket] = now.Add(5 * time.Minute)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]string{"url": "/api/v1/rig-os/image?ticket=" + ticket})
}

func (s *HTTPServer) downloadRigOSImage(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	s.mu.RLock()
	expiresAt, ok := s.downloads[ticket]
	s.mu.RUnlock()
	if ticket == "" || !ok || !expiresAt.After(time.Now()) {
		writeError(w, ErrUnauthorized)
		return
	}
	s.mu.Lock()
	delete(s.downloads, ticket)
	s.mu.Unlock()
	status := s.store.RigOSStatus()
	if !status.ImageAvailable {
		writeError(w, ErrNotFound)
		return
	}
	path := filepath.Join(filepath.Dir(s.store.path), "rig-os", rigOSImageFilename)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, ErrNotFound)
		} else {
			writeError(w, err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/x-xz")
	w.Header().Set("X-Content-SHA256", status.ImageSHA256)
	w.Header().Set("Content-Disposition", `attachment; filename="`+rigOSImageFilename+`"`)
	http.ServeFile(w, r, path)
}
