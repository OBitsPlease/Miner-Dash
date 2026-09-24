package server

import (
	"crypto/sha256"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"minerdash/internal/buildinfo"
	"minerdash/internal/protocol"
)

//go:embed web/*
var webFiles embed.FS

type HTTPServer struct {
	store      *Store
	logger     *log.Logger
	mu         sync.RWMutex
	market     *marketProvider
	poolStats  *poolStatsProvider
	releases   *catalogReleaseMonitor
	community  *communityMinerRegistry
	appUpdates *applicationUpdateMonitor
	usb        *usbManager
	sessions   map[string]session
	downloads  map[string]time.Time
	recovery   map[string]recoveryAttempt
}

type session struct {
	UserID    string
	ExpiresAt time.Time
}

type recoveryAttempt struct {
	WindowStart time.Time
	Count       int
}

func NewHTTPServer(store *Store, logger *log.Logger) http.Handler {
	appUpdates := newApplicationUpdateMonitor()
	s := &HTTPServer{
		store: store, logger: logger,
		market: newMarketProvider(), poolStats: newPoolStatsProvider(), releases: newCatalogReleaseMonitor(), community: newCommunityMinerRegistry(), appUpdates: appUpdates,
		usb:      newUSBManager(filepath.Dir(store.path), appUpdates, store),
		sessions: make(map[string]session), downloads: make(map[string]time.Time), recovery: make(map[string]recoveryAttempt),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": buildinfo.Version})
	})
	mux.HandleFunc("POST /api/v1/login", s.login)
	mux.HandleFunc("POST /api/v1/password-recovery", s.passwordRecovery)
	mux.HandleFunc("POST /api/v1/logout", s.readAuth(s.logout))
	mux.HandleFunc("POST /api/v1/enroll", s.enroll)
	mux.HandleFunc("POST /api/v1/agents/{id}/heartbeat", s.agentAuth(s.heartbeat))
	mux.HandleFunc("GET /api/v1/agents/{id}/configuration", s.agentAuth(s.agentConfiguration))
	mux.HandleFunc("GET /api/v1/agents/{id}/miners/{minerID}/binary", s.agentAuth(s.agentMinerBinary))
	mux.HandleFunc("GET /api/v1/agents/{id}/release/{architecture}", s.agentAuth(s.agentRelease))
	mux.HandleFunc("GET /api/v1/agents/{id}/commands/next", s.agentAuth(s.nextCommand))
	mux.HandleFunc("POST /api/v1/agents/{id}/commands/{commandID}/result", s.agentAuth(s.commandResult))
	mux.HandleFunc("GET /api/v1/rigs", s.readAuth(s.listRigs))
	mux.HandleFunc("DELETE /api/v1/rigs/{id}", s.adminAuth(s.deleteRig))
	mux.HandleFunc("POST /api/v1/rigs/commands", s.operatorAuth(s.addBulkCommand))
	mux.HandleFunc("POST /api/v1/rigs/{id}/commands", s.operatorAuth(s.addCommand))
	mux.HandleFunc("GET /api/v1/rigs/{id}/commands", s.readAuth(s.listCommands))
	mux.HandleFunc("PUT /api/v1/rigs/{id}/assignment", s.operatorAuth(s.assignWorker))
	mux.HandleFunc("GET /api/v1/rigs/{id}/history", s.readAuth(s.workerHistory))
	mux.HandleFunc("GET /api/v1/rigs/{id}/pool-stats", s.readAuth(s.workerPoolStats))
	mux.HandleFunc("GET /api/v1/coin-prices", s.readAuth(s.coinPrices))
	mux.HandleFunc("GET /api/v1/coin-assets", s.readAuth(s.coinAssets))
	mux.HandleFunc("GET /coin-logo/{symbol}", s.coinLogo)
	mux.HandleFunc("GET /api/v1/farms", s.readAuth(s.listFarms))
	mux.HandleFunc("POST /api/v1/farms", s.operatorAuth(s.saveFarm))
	mux.HandleFunc("PUT /api/v1/farms/{id}", s.operatorAuth(s.saveFarm))
	mux.HandleFunc("DELETE /api/v1/farms/{id}", s.operatorAuth(s.deleteResource("farms")))
	mux.HandleFunc("GET /api/v1/wallets", s.readAuth(s.listWallets))
	mux.HandleFunc("POST /api/v1/wallets", s.operatorAuth(s.saveWallet))
	mux.HandleFunc("PUT /api/v1/wallets/{id}", s.operatorAuth(s.saveWallet))
	mux.HandleFunc("DELETE /api/v1/wallets/{id}", s.operatorAuth(s.deleteResource("wallets")))
	mux.HandleFunc("GET /api/v1/pools", s.readAuth(s.listPools))
	mux.HandleFunc("POST /api/v1/pools", s.operatorAuth(s.savePool))
	mux.HandleFunc("PUT /api/v1/pools/{id}", s.operatorAuth(s.savePool))
	mux.HandleFunc("DELETE /api/v1/pools/{id}", s.operatorAuth(s.deleteResource("pools")))
	mux.HandleFunc("GET /api/v1/miners", s.readAuth(s.listMiners))
	mux.HandleFunc("POST /api/v1/miners", s.operatorAuth(s.saveMiner))
	mux.HandleFunc("PUT /api/v1/miners/{id}", s.operatorAuth(s.saveMiner))
	mux.HandleFunc("DELETE /api/v1/miners/{id}", s.operatorAuth(s.deleteResource("miners")))
	mux.HandleFunc("PUT /api/v1/miners/{id}/binary", s.adminAuth(s.uploadMinerBinary))
	mux.HandleFunc("POST /api/v1/miners/import-url", s.operatorAuth(s.importCustomMiner))
	mux.HandleFunc("GET /api/v1/community-miners", s.readAuth(s.communityMiners))
	mux.HandleFunc("POST /api/v1/community-miners/{minerID}", s.adminAuth(s.installCommunityMiner))
	mux.HandleFunc("GET /api/v1/rig-os", s.readAuth(s.rigOSStatus))
	mux.HandleFunc("POST /api/v1/rig-os/image-ticket", s.readAuth(s.createRigOSImageTicket))
	mux.HandleFunc("GET /api/v1/rig-os/image", s.downloadRigOSImage)
	mux.HandleFunc("GET /api/v1/rig-os/usb", s.adminAuth(s.usbStatus))
	mux.HandleFunc("POST /api/v1/rig-os/image-download", s.adminAuth(s.downloadRigOSImageFromRelease))
	mux.HandleFunc("POST /api/v1/rig-os/usb/flash", s.adminAuth(s.flashRigOSUSB))
	mux.HandleFunc("POST /api/v1/rig-os/usb/pair", s.adminAuth(s.pairRigOSUSB))
	mux.HandleFunc("POST /api/v1/rig-os/usb/restore", s.adminAuth(s.restoreRigOSUSB))
	mux.HandleFunc("POST /api/v1/rig-os/miners/{catalogID}", s.adminAuth(s.installCatalogMiner))
	mux.HandleFunc("GET /api/v1/flight-sheets", s.readAuth(s.listFlightSheets))
	mux.HandleFunc("POST /api/v1/flight-sheets", s.operatorAuth(s.saveFlightSheet))
	mux.HandleFunc("POST /api/v1/quick-flight-sheet", s.operatorAuth(s.saveQuickFlightSheet))
	mux.HandleFunc("PUT /api/v1/flight-sheets/{id}", s.operatorAuth(s.saveFlightSheet))
	mux.HandleFunc("DELETE /api/v1/flight-sheets/{id}", s.operatorAuth(s.deleteResource("flight-sheets")))
	mux.HandleFunc("GET /api/v1/overclock-profiles", s.readAuth(s.listOverclockProfiles))
	mux.HandleFunc("POST /api/v1/overclock-profiles", s.operatorAuth(s.saveOverclockProfile))
	mux.HandleFunc("PUT /api/v1/overclock-profiles/{id}", s.operatorAuth(s.saveOverclockProfile))
	mux.HandleFunc("DELETE /api/v1/overclock-profiles/{id}", s.operatorAuth(s.deleteResource("overclock-profiles")))
	mux.HandleFunc("GET /api/v1/activity", s.readAuth(s.listActivities))
	mux.HandleFunc("GET /api/v1/alerts", s.readAuth(s.listAlerts))
	mux.HandleFunc("GET /api/v1/schedules", s.readAuth(s.listSchedules))
	mux.HandleFunc("POST /api/v1/schedules", s.operatorAuth(s.saveSchedule))
	mux.HandleFunc("PUT /api/v1/schedules/{id}", s.operatorAuth(s.saveSchedule))
	mux.HandleFunc("DELETE /api/v1/schedules/{id}", s.operatorAuth(s.deleteResource("schedules")))
	mux.HandleFunc("GET /api/v1/users", s.adminAuth(s.listUsers))
	mux.HandleFunc("POST /api/v1/users", s.adminAuth(s.saveUser))
	mux.HandleFunc("PUT /api/v1/users/{id}", s.adminAuth(s.saveUser))
	mux.HandleFunc("DELETE /api/v1/users/{id}", s.adminAuth(s.deleteUser))
	mux.HandleFunc("GET /api/v1/recovery-code", s.adminAuth(s.recoveryCode))
	mux.HandleFunc("GET /api/v1/enrollment-token", s.adminAuth(s.enrollmentToken))
	mux.HandleFunc("GET /api/v1/system-update", s.readAuth(s.applicationUpdateStatus))
	mux.HandleFunc("POST /api/v1/system-update", s.adminAuth(s.installApplicationUpdate))
	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(webRoot)))
	return securityHeaders(mux)
}

func (s *HTTPServer) enroll(w http.ResponseWriter, r *http.Request) {
	var request protocol.EnrollRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	response, err := s.store.Enroll(request.Name, request.EnrollmentToken)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *HTTPServer) heartbeat(w http.ResponseWriter, r *http.Request) {
	var request protocol.HeartbeatRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.store.Heartbeat(r.PathValue("id"), request.Metrics, remoteIPAddress(r.RemoteAddr)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func remoteIPAddress(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func (s *HTTPServer) agentConfiguration(w http.ResponseWriter, r *http.Request) {
	configuration, err := s.store.ResolvedWorkerConfiguration(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configuration)
}

func (s *HTTPServer) agentMinerBinary(w http.ResponseWriter, r *http.Request) {
	path, miner, err := s.store.MinerBinaryForRig(r.PathValue("id"), r.PathValue("minerID"))
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	digest := miner.BinarySHA256
	filename := miner.BinaryName
	if miner.PackageFormat == "tar.gz" {
		digest = miner.PackageSHA256
		filename = miner.ID + ".tar.gz"
	}
	w.Header().Set("X-Content-SHA256", digest)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	http.ServeFile(w, r, path)
}

func (s *HTTPServer) agentRelease(w http.ResponseWriter, r *http.Request) {
	architecture := r.PathValue("architecture")
	path, err := s.store.AgentReleasePath(architecture)
	if err != nil {
		writeError(w, err)
		return
	}
	digest, err := packageFileSHA256(path)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-SHA256", digest)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "minerdash-agent-"+architecture))
	http.ServeFile(w, r, path)
}

func (s *HTTPServer) nextCommand(w http.ResponseWriter, r *http.Request) {
	command, err := s.store.NextCommand(r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, command)
}

func (s *HTTPServer) commandResult(w http.ResponseWriter, r *http.Request) {
	var result protocol.CommandResult
	if !decodeJSON(w, r, &result) {
		return
	}
	err := s.store.FinishCommandResult(r.PathValue("id"), r.PathValue("commandID"), result)
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) listRigs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListRigs(time.Now().UTC()))
}

func (s *HTTPServer) deleteRig(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRig(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) addCommand(w http.ResponseWriter, r *http.Request) {
	var request protocol.CommandRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	command, err := s.store.AddCommandRequest(r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, command)
}

func (s *HTTPServer) addBulkCommand(w http.ResponseWriter, r *http.Request) {
	var request protocol.BulkCommandRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	commands, err := s.store.AddCommands(request.RigIDs, request.Action, request.Profile)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, commands)
}

func (s *HTTPServer) listCommands(w http.ResponseWriter, r *http.Request) {
	commands, err := s.store.ListCommands(r.PathValue("id"), 20)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commands)
}

func (s *HTTPServer) assignWorker(w http.ResponseWriter, r *http.Request) {
	var assignment protocol.WorkerAssignment
	if !decodeJSON(w, r, &assignment) {
		return
	}
	rig, err := s.store.AssignWorker(r.PathValue("id"), assignment)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rig)
}

func (s *HTTPServer) listFarms(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListFarms())
}

func (s *HTTPServer) saveFarm(w http.ResponseWriter, r *http.Request) {
	var value protocol.Farm
	if !decodeJSON(w, r, &value) {
		return
	}
	value.ID = requestResourceID(r, value.ID)
	saved, err := s.store.SaveFarm(value)
	writeSaved(w, saved, err, r.Method)
}

func (s *HTTPServer) listWallets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListWallets())
}

func (s *HTTPServer) saveWallet(w http.ResponseWriter, r *http.Request) {
	var value protocol.Wallet
	if !decodeJSON(w, r, &value) {
		return
	}
	value.ID = requestResourceID(r, value.ID)
	saved, err := s.store.SaveWallet(value)
	writeSaved(w, saved, err, r.Method)
}

func (s *HTTPServer) listPools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListPools())
}

func (s *HTTPServer) savePool(w http.ResponseWriter, r *http.Request) {
	var value protocol.Pool
	if !decodeJSON(w, r, &value) {
		return
	}
	value.ID = requestResourceID(r, value.ID)
	saved, err := s.store.SavePool(value)
	writeSaved(w, saved, err, r.Method)
}

func (s *HTTPServer) listMiners(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListMiners())
}

func (s *HTTPServer) saveMiner(w http.ResponseWriter, r *http.Request) {
	var value protocol.MinerDefinition
	if !decodeJSON(w, r, &value) {
		return
	}
	value.ID = requestResourceID(r, value.ID)
	saved, err := s.store.SaveMiner(value)
	writeSaved(w, saved, err, r.Method)
}

func (s *HTTPServer) uploadMinerBinary(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	saved, err := s.store.SaveMinerPackage(r.PathValue("id"), r.Body)
	writeSaved(w, saved, err, http.MethodPut)
}

func (s *HTTPServer) listFlightSheets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListFlightSheets())
}

func (s *HTTPServer) saveFlightSheet(w http.ResponseWriter, r *http.Request) {
	var value protocol.FlightSheet
	if !decodeJSON(w, r, &value) {
		return
	}
	value.ID = requestResourceID(r, value.ID)
	saved, err := s.store.SaveFlightSheet(value)
	writeSaved(w, saved, err, r.Method)
}

func (s *HTTPServer) saveQuickFlightSheet(w http.ResponseWriter, r *http.Request) {
	var value protocol.QuickFlightSheetInput
	if !decodeJSON(w, r, &value) {
		return
	}
	if value.CatalogID != "" {
		if _, err := s.store.EnsureCatalogMinerPackage(r.Context(), value.CatalogID, false); err != nil {
			writeError(w, err)
			return
		}
	}
	saved, err := s.store.SaveQuickFlightSheet(value)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *HTTPServer) listOverclockProfiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListOverclockProfiles())
}

func (s *HTTPServer) saveOverclockProfile(w http.ResponseWriter, r *http.Request) {
	var value protocol.OverclockProfile
	if !decodeJSON(w, r, &value) {
		return
	}
	value.ID = requestResourceID(r, value.ID)
	saved, err := s.store.SaveOverclockProfile(value)
	writeSaved(w, saved, err, r.Method)
}

func (s *HTTPServer) listActivities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListActivities(100))
}

func (s *HTTPServer) workerHistory(w http.ResponseWriter, r *http.Request) {
	until := time.Now().UTC()
	if raw := r.URL.Query().Get("until"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "until must be RFC3339", http.StatusBadRequest)
			return
		}
		until = parsed
	}
	since := until.Add(-24 * time.Hour)
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "since must be RFC3339", http.StatusBadRequest)
			return
		}
		since = parsed
	}
	if !until.After(since) || until.Sub(since) > 33*24*time.Hour {
		http.Error(w, "history range must be greater than zero and no more than 33 days", http.StatusBadRequest)
		return
	}
	maxPoints := 2000
	if raw := r.URL.Query().Get("max_points"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 100 || parsed > 5000 {
			http.Error(w, "max_points must be between 100 and 5000", http.StatusBadRequest)
			return
		}
		maxPoints = parsed
	}
	history, err := s.store.WorkerHistory(r.PathValue("id"), since, until, maxPoints)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *HTTPServer) listAlerts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListAlerts(r.URL.Query().Get("active") == "true"))
}

func (s *HTTPServer) listSchedules(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListSchedules())
}

func (s *HTTPServer) saveSchedule(w http.ResponseWriter, r *http.Request) {
	var value protocol.Schedule
	if !decodeJSON(w, r, &value) {
		return
	}
	value.ID = requestResourceID(r, value.ID)
	saved, err := s.store.SaveSchedule(value)
	writeSaved(w, saved, err, r.Method)
}

func requestResourceID(r *http.Request, bodyID string) string {
	pathID := r.PathValue("id")
	if pathID != "" {
		return pathID
	}
	return bodyID
}

func writeSaved[T any](w http.ResponseWriter, value T, err error, method string) {
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, value)
}

func (s *HTTPServer) deleteResource(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.DeleteResource(kind, r.PathValue("id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *HTTPServer) login(w http.ResponseWriter, r *http.Request) {
	var request protocol.LoginRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := s.store.AuthenticateUser(request.Username, request.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, err)
		return
	}
	s.mu.Lock()
	s.sessions[token] = session{UserID: user.ID, ExpiresAt: time.Now().UTC().Add(12 * time.Hour)}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, protocol.LoginResponse{Token: token, User: user})
}

func (s *HTTPServer) passwordRecovery(w http.ResponseWriter, r *http.Request) {
	if !s.allowPasswordRecovery(r.RemoteAddr) {
		http.Error(w, "too many recovery attempts; try again in 15 minutes", http.StatusTooManyRequests)
		return
	}
	var request protocol.PasswordRecoveryRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := s.store.ResetUserPassword(request.Username, request.NewPassword, request.RecoveryCode)
	if err != nil {
		writeError(w, err)
		return
	}
	s.mu.Lock()
	for token, current := range s.sessions {
		if current.UserID == user.ID {
			delete(s.sessions, token)
		}
	}
	delete(s.recovery, remoteHost(r.RemoteAddr))
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) allowPasswordRecovery(remoteAddress string) bool {
	host := remoteHost(remoteAddress)
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt := s.recovery[host]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) >= 15*time.Minute {
		attempt = recoveryAttempt{WindowStart: now}
	}
	if attempt.Count >= 5 {
		return false
	}
	attempt.Count++
	s.recovery[host] = attempt
	return true
}

func remoteHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}

func (s *HTTPServer) logout(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	delete(s.sessions, bearerToken(r))
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) listUsers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListUsers())
}

func (s *HTTPServer) saveUser(w http.ResponseWriter, r *http.Request) {
	var input protocol.UserInput
	if !decodeJSON(w, r, &input) {
		return
	}
	saved, err := s.store.SaveUser(r.PathValue("id"), input)
	writeSaved(w, saved, err, r.Method)
}

func (s *HTTPServer) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteUser(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) recoveryCode(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"recovery_code": s.store.RecoveryCode(),
		"usage":         "Store this code securely. It can reset any local Mining Dash account without internet access.",
	})
}

func (s *HTTPServer) enrollmentToken(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]string{{
		"token":       s.store.EnrollmentToken(),
		"fingerprint": s.store.TLSFingerprint(),
		"usage":       "Copy this token and TLS fingerprint into a new rig's /etc/minerdash/agent.json for its first connection.",
	}})
}

func (s *HTTPServer) adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.roleAuth("admin", next)
}

func (s *HTTPServer) operatorAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.roleAuth("operator", next)
}

func (s *HTTPServer) readAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.roleAuth("viewer", next)
}

func (s *HTTPServer) roleAuth(required string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if !s.authorized(token, required) {
			writeError(w, ErrUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *HTTPServer) authorized(token, required string) bool {
	if s.store.IsAdmin(token) {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[token]
	if !ok {
		return false
	}
	if time.Now().UTC().After(current.ExpiresAt) {
		delete(s.sessions, token)
		return false
	}
	user, ok := s.store.UserByID(current.UserID)
	if !ok || user.Disabled {
		delete(s.sessions, token)
		return false
	}
	return roleRank(user.Role) >= roleRank(required)
}

func roleRank(role string) int {
	switch role {
	case "admin":
		return 3
	case "operator":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func (s *HTTPServer) agentAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.AuthenticateAgent(r.PathValue("id"), bearerToken(r)); err != nil {
			writeError(w, err)
			return
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return token
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "request body must contain exactly one JSON value", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func CertificateFingerprint(certFile string) (string, error) {
	data, err := osReadFile(certFile)
	if err != nil {
		return "", err
	}
	block, _ := pemDecode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("%s does not contain a certificate", certFile)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
}
