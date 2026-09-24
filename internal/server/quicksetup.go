package server

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"minerdash/internal/protocol"
)

func (s *Store) SaveQuickFlightSheet(input protocol.QuickFlightSheetInput) (protocol.QuickFlightSheetResult, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.DeviceType = strings.ToUpper(strings.TrimSpace(input.DeviceType))
	input.Coin = strings.ToUpper(strings.TrimSpace(input.Coin))
	input.WalletAddress = strings.TrimSpace(input.WalletAddress)
	input.PoolURL = strings.TrimSpace(input.PoolURL)
	input.Algorithm = strings.TrimSpace(input.Algorithm)
	input.WalletTemplate = strings.TrimSpace(input.WalletTemplate)
	input.SecondaryCoin = strings.ToUpper(strings.TrimSpace(input.SecondaryCoin))
	input.SecondaryDeviceType = strings.ToUpper(strings.TrimSpace(input.SecondaryDeviceType))
	input.SecondaryWalletAddress = strings.TrimSpace(input.SecondaryWalletAddress)
	input.SecondaryPoolURL = strings.TrimSpace(input.SecondaryPoolURL)
	input.SecondaryAlgorithm = strings.TrimSpace(input.SecondaryAlgorithm)
	if input.Name == "" || input.Coin == "" || input.WalletAddress == "" || input.PoolURL == "" || input.Algorithm == "" {
		return protocol.QuickFlightSheetResult{}, errors.New("name, coin, wallet address, pool URL, and algorithm are required")
	}
	if input.DeviceType != "CPU" && input.DeviceType != "GPU" {
		return protocol.QuickFlightSheetResult{}, errors.New("device type must be CPU or GPU")
	}
	if strings.ContainsAny(input.WalletAddress, " \t\r\n") {
		return protocol.QuickFlightSheetResult{}, errors.New("wallet address cannot contain whitespace")
	}
	parsedPool, err := url.Parse(input.PoolURL)
	if err != nil || parsedPool.Host == "" || (parsedPool.Scheme != "stratum+tcp" && parsedPool.Scheme != "stratum+ssl" && parsedPool.Scheme != "stratum+tls") {
		return protocol.QuickFlightSheetResult{}, errors.New("pool URL must use stratum+tcp, stratum+ssl, or stratum+tls")
	}
	if (input.MinerID == "") == (input.CatalogID == "") {
		return protocol.QuickFlightSheetResult{}, errors.New("choose one mining software option")
	}
	hasSecondary := input.SecondaryDeviceType != "" || input.SecondaryCoin != "" || input.SecondaryWalletAddress != "" || input.SecondaryPoolURL != "" || input.SecondaryAlgorithm != ""
	var secondaryPoolURL *url.URL
	if hasSecondary {
		if input.SecondaryCoin == "" || input.SecondaryWalletAddress == "" || input.SecondaryPoolURL == "" || input.SecondaryAlgorithm == "" {
			return protocol.QuickFlightSheetResult{}, errors.New("secondary coin, wallet, pool URL, and algorithm are all required")
		}
		if input.SecondaryDeviceType != "CPU" && input.SecondaryDeviceType != "GPU" {
			return protocol.QuickFlightSheetResult{}, errors.New("secondary device type must be CPU or GPU")
		}
		if input.SecondaryDeviceType == input.DeviceType {
			return protocol.QuickFlightSheetResult{}, errors.New("primary and secondary workloads must use different device types")
		}
		if strings.ContainsAny(input.SecondaryWalletAddress, " \t\r\n") {
			return protocol.QuickFlightSheetResult{}, errors.New("secondary wallet address cannot contain whitespace")
		}
		secondaryPoolURL, err = url.Parse(input.SecondaryPoolURL)
		if err != nil || secondaryPoolURL.Host == "" || (secondaryPoolURL.Scheme != "stratum+tcp" && secondaryPoolURL.Scheme != "stratum+ssl" && secondaryPoolURL.Scheme != "stratum+tls") {
			return protocol.QuickFlightSheetResult{}, errors.New("secondary pool URL must use stratum+tcp, stratum+ssl, or stratum+tls")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rigID := range input.RigIDs {
		if _, ok := s.state.Rigs[rigID]; !ok {
			return protocol.QuickFlightSheetResult{}, errors.New("selected worker does not exist")
		}
	}

	var miner protocol.MinerDefinition
	if input.MinerID != "" {
		var ok bool
		miner, ok = s.state.Miners[input.MinerID]
		if !ok {
			return protocol.QuickFlightSheetResult{}, errors.New("selected mining software does not exist")
		}
	} else {
		entry, ok := catalogEntry(input.CatalogID)
		if !ok {
			return protocol.QuickFlightSheetResult{}, errors.New("selected mining software is not in the catalog")
		}
		supportsDevice := false
		for _, hardware := range entry.Hardware {
			if (input.DeviceType == "CPU" && hardware == "CPU") || (input.DeviceType == "GPU" && hardware != "CPU") {
				supportsDevice = true
				break
			}
		}
		if !supportsDevice {
			return protocol.QuickFlightSheetResult{}, errors.New("selected mining software does not support this workload type")
		}
		for _, existing := range s.state.Miners {
			if catalogMinerMatches(entry, existing) {
				miner = existing
				break
			}
		}
		if miner.ID == "" {
			miner = catalogMinerDefinition(entry)
			miner.ID = mustToken(12)
			miner.CreatedAt = time.Now().UTC()
			s.state.Miners[miner.ID] = miner
		}
	}
	if entry, ok := catalogEntry(miner.CatalogID); ok {
		supportsDevice := false
		for _, hardware := range entry.Hardware {
			if (input.DeviceType == "CPU" && hardware == "CPU") || (input.DeviceType == "GPU" && hardware != "CPU") {
				supportsDevice = true
				break
			}
		}
		if !supportsDevice {
			return protocol.QuickFlightSheetResult{}, errors.New("selected mining software does not support this workload type")
		}
		if hasSecondary && miner.CatalogID != "srbminer-multi" {
			return protocol.QuickFlightSheetResult{}, errors.New("CPU and GPU dual workloads currently require SRBMiner-MULTI")
		}
	} else if !strings.EqualFold(miner.Algorithm, input.Algorithm) {
		supportsAlgorithm := false
		for _, argument := range miner.ExtraArguments {
			if strings.Contains(argument, "{ALGORITHM}") {
				supportsAlgorithm = true
				break
			}
		}
		if !supportsAlgorithm {
			return protocol.QuickFlightSheetResult{}, errors.New("custom mining software must use {ALGORITHM} in its advanced arguments")
		}
	}

	now := time.Now().UTC()
	var wallet protocol.Wallet
	for _, existing := range s.state.Wallets {
		if strings.EqualFold(existing.Coin, input.Coin) && existing.Address == input.WalletAddress {
			wallet = existing
			break
		}
	}
	if wallet.ID == "" {
		wallet = protocol.Wallet{ID: mustToken(12), Name: input.Coin + " Wallet", Coin: input.Coin, Address: input.WalletAddress, CreatedAt: now}
		s.state.Wallets[wallet.ID] = wallet
	}

	var pool protocol.Pool
	for _, existing := range s.state.Pools {
		if existing.URL == input.PoolURL && existing.Password == input.PoolPassword {
			pool = existing
			break
		}
	}
	if pool.ID == "" {
		pool = protocol.Pool{ID: mustToken(12), Name: parsedPool.Host, URL: input.PoolURL, Password: input.PoolPassword, CreatedAt: now}
		s.state.Pools[pool.ID] = pool
	}

	var secondaryWallet protocol.Wallet
	var secondaryPool protocol.Pool
	if hasSecondary {
		for _, existing := range s.state.Wallets {
			if strings.EqualFold(existing.Coin, input.SecondaryCoin) && existing.Address == input.SecondaryWalletAddress {
				secondaryWallet = existing
				break
			}
		}
		if secondaryWallet.ID == "" {
			secondaryWallet = protocol.Wallet{ID: mustToken(12), Name: input.SecondaryCoin + " Wallet", Coin: input.SecondaryCoin, Address: input.SecondaryWalletAddress, CreatedAt: now}
			s.state.Wallets[secondaryWallet.ID] = secondaryWallet
		}
		for _, existing := range s.state.Pools {
			if existing.URL == input.SecondaryPoolURL && existing.Password == input.SecondaryPoolPassword {
				secondaryPool = existing
				break
			}
		}
		if secondaryPool.ID == "" {
			secondaryPool = protocol.Pool{ID: mustToken(12), Name: secondaryPoolURL.Host, URL: input.SecondaryPoolURL, Password: input.SecondaryPoolPassword, CreatedAt: now}
			s.state.Pools[secondaryPool.ID] = secondaryPool
		}
	}

	sheet := protocol.FlightSheet{
		ID: mustToken(12), Name: input.Name, DeviceType: input.DeviceType,
		Coin: input.Coin, Algorithm: input.Algorithm, WalletID: wallet.ID,
		PoolID: pool.ID, MinerID: miner.ID, CreatedAt: now,
		WalletTemplate: input.WalletTemplate,
		SecondaryCoin:  input.SecondaryCoin, SecondaryDeviceType: input.SecondaryDeviceType,
		SecondaryAlgorithm: input.SecondaryAlgorithm, SecondaryWalletID: secondaryWallet.ID,
		SecondaryPoolID: secondaryPool.ID,
	}
	s.state.FlightSheets[sheet.ID] = sheet
	for _, rigID := range input.RigIDs {
		rig := s.state.Rigs[rigID]
		rig.Desired.FlightSheetID = sheet.ID
		rig.Desired.Revision++
	}
	s.recordLocked("quick-setup", "flight-sheet", sheet.ID, map[string]any{
		"name": sheet.Name, "device_type": sheet.DeviceType, "coin": sheet.Coin,
		"miner": miner.Name, "workers": len(input.RigIDs),
	})
	if err := s.saveLocked(); err != nil {
		return protocol.QuickFlightSheetResult{}, err
	}
	return protocol.QuickFlightSheetResult{
		FlightSheet: sheet,
		MinerReady:  (!miner.ManagedBinary && miner.Profile != "") || (miner.ManagedBinary && miner.BinarySHA256 != ""),
		Assigned:    len(input.RigIDs),
	}, nil
}
