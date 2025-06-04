package main

import (
	"context"
	"crypto/tls"
	// "crypto/x509" // Not directly used here
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filesync/internal/config"
	"filesync/internal/core"
	"filesync/internal/crypto"
	"github.com/grandcat/zeroconf"
)

type ServerResponseToInitialClientList struct {
	FilesToRequestFromClient []string          `json:"filesToRequestFromClient"`
	ServerCompleteFileList   map[string]string `json:"serverCompleteFileList"`
}

func handleInitCmd(folderPath string) {
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving absolute path for folder '%s': %v. Using provided path.\n", folderPath, err)
		absPath = folderPath
	}
	fmt.Printf("Initializing folder at '%s'...\n", absPath)

	cfg, err := config.LoadConfig(absPath)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error loading configuration for folder '%s': %v\n", absPath, err)
		}
	}

	err = config.SaveConfig(absPath, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error saving configuration for folder '%s': %v\n", absPath, err)
		os.Exit(1)
	}
	fmt.Printf("Folder '%s' initialized successfully. DeviceID: %s\n", absPath, cfg.DeviceID)
}

func handleAddDeviceCmd(folderPath, name, address, key string, port int) {
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving absolute path for folder '%s': %v. Using provided path.\n", folderPath, err)
		absPath = folderPath
	}
	fmt.Printf("Adding device to configuration in folder '%s':\n", absPath)
	fmt.Printf("  Name:    %s\n", name)
	fmt.Printf("  Address: %s\n", address)
	fmt.Printf("  Port:    %d\n", port)

	cfg, err := config.LoadConfig(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration from '%s': %v\n", absPath, err)
	}
	if cfg.Devices == nil {
		cfg.Devices = make(map[string]config.DeviceConfig)
	}

	newDevice := config.DeviceConfig{
		Name:    name,
		Address: address,
		Port:    port,
		Key:     key,
	}
	cfg.Devices[name] = newDevice

	err = config.SaveConfig(absPath, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error saving updated configuration to '%s': %v\n", absPath, err)
		os.Exit(1)
	}
	fmt.Printf("Device '%s' added/updated in configuration for folder '%s'.\n", name, absPath)
}

func sendPrefixedMessage(conn net.Conn, msgType string, data []byte) error {
	lenBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(lenBuf, uint64(len(data)))
	log.Printf("P2P: Sending %s length: %d bytes", msgType, len(data))
	if _, err := conn.Write(lenBuf); err != nil {
		return fmt.Errorf("error sending %s length: %w", msgType, err)
	}
	log.Printf("P2P: Sending %s data...", msgType)
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("error sending %s data: %w", msgType, err)
	}
	log.Printf("P2P: %s sent successfully.", msgType)
	return nil
}

func receivePrefixedMessage(conn net.Conn, msgType string) ([]byte, error) {
	lenBuf := make([]byte, 8)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, fmt.Errorf("error reading %s length: %w", msgType, err)
	}
	dataLen := binary.BigEndian.Uint64(lenBuf)
	log.Printf("P2P: Received %s length: %d", msgType, dataLen)

	if dataLen > 256*1024*1024 { // 256MB sanity limit
		return nil, fmt.Errorf("%s length %d seems too large", msgType, dataLen)
	}
	dataBuf := make([]byte, dataLen)
	if _, err := io.ReadFull(conn, dataBuf); err != nil {
		return nil, fmt.Errorf("error reading %s data: %w", msgType, err)
	}
	log.Printf("P2P: %s data received successfully (%d bytes).", msgType, len(dataBuf))
	return dataBuf, nil
}

func handleSyncCmd(folderPath, peerInstanceName string) {
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		log.Printf("Error resolving absolute path for sync folder '%s': %v.", folderPath, err)
		absPath = folderPath
	}
	log.Printf("P2P Sync: Initiating with peer '%s' for folder '%s'", peerInstanceName, absPath)

	resolver, err := zeroconf.NewResolver(zeroconf.SelectIPTraffic(zeroconf.IPv4))
	if err != nil { log.Printf("P2P Sync: Failed to initialize mDNS resolver: %v", err); return }

	entries := make(chan *zeroconf.ServiceEntry)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	var peerEntry *zeroconf.ServiceEntry
	foundPeer := false

	go func(ctxLookup context.Context) {
		defer close(entries)
		errLookup := resolver.Lookup(ctxLookup, peerInstanceName, "_filesync._tcp", "local.", entries)
		if errLookup != nil { log.Printf("P2P Sync: mDNS Lookup error for '%s': %v", peerInstanceName, errLookup) }
		<-ctxLookup.Done()
	}(ctx)

	log.Printf("P2P Sync: Attempting to lookup peer '%s'...", peerInstanceName)
	for entry := range entries {
		if entry.Instance == peerInstanceName {
			if len(entry.AddrIPv4) == 0 && len(entry.AddrIPv6) == 0 { continue }
			entryCopy := *entry; peerEntry = &entryCopy; foundPeer = true; cancel(); break
		}
	}
	if !foundPeer { log.Printf("P2P Sync: Peer '%s' not found or timed out.", peerInstanceName); return }

	var chosenIPString string
	if len(peerEntry.AddrIPv4) > 0 { chosenIPString = peerEntry.AddrIPv4[0].String() } else { chosenIPString = peerEntry.AddrIPv6[0].String() }
	targetAddr := fmt.Sprintf("%s:%d", chosenIPString, peerEntry.Port)
	if strings.Contains(chosenIPString, ":") { targetAddr = fmt.Sprintf("[%s]:%d", chosenIPString, peerEntry.Port) }

	localCfgOur, err := config.LoadConfig(absPath)
	if err != nil { log.Printf("P2P Sync: Error loading local config from '%s' for TLS: %v", absPath, err); return }
	if localCfgOur.DeviceID == "" { log.Printf("P2P Sync: DeviceID empty in '%s'. Run 'init'.", absPath); return }

	clientTlsConfig, err := crypto.EnsureTLSConfig(filepath.Join(absPath, ".filesync"), localCfgOur.DeviceID)
	if err != nil { log.Printf("P2P Sync: Error ensuring local TLS config: %v", err); return }
	clientTlsConfig.InsecureSkipVerify = true

	log.Printf("P2P Sync: Attempting TLS connection to peer '%s' at %s...", peerInstanceName, targetAddr)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tlsConn, err := tls.DialWithDialer(dialer, "tcp", targetAddr, clientTlsConfig)
	if err != nil { log.Printf("P2P Sync: Error establishing TLS to '%s': %v", peerInstanceName, err); return }
	defer tlsConn.Close()
	if err := tlsConn.Handshake(); err != nil { log.Printf("P2P Sync: TLS handshake failed with '%s': %v", peerInstanceName, err); return }
	log.Printf("P2P Sync: TLS handshake successful with '%s'.", peerInstanceName)
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) > 0 {
		fp := crypto.GetCertFingerprint(state.PeerCertificates[0])
		log.Printf("P2P Sync: Peer '%s' cert subject: %s, Fingerprint: %s", peerInstanceName, state.PeerCertificates[0].Subject, fp)
	} else { log.Printf("P2P Sync: Peer '%s' did not present a certificate.", peerInstanceName) }

	conn := net.Conn(tlsConn) // Use net.Conn for subsequent generic operations

	// Phase 1: Client sends its file list
	log.Printf("P2P Sync: Scanning local folder '%s'...", absPath)
	localFiles, err := core.ScanFolder(absPath, ".filesync")
	if err != nil { log.Printf("P2P Sync: Error scanning folder '%s': %v", absPath, err); return }
	log.Printf("P2P Sync: Scanned %d local files.", len(localFiles))
	jsonData, err := json.Marshal(localFiles)
	if err != nil { log.Printf("P2P Sync: Error marshalling local file list: %v", err); return }
	if err := sendPrefixedMessage(conn, "initial client file list", jsonData); err != nil {
		log.Printf("P2P Sync: Error sending local file list: %v", err); return
	}
	log.Printf("P2P Sync: Sent my file list (%d files) to '%s'", len(localFiles), peerInstanceName)

	// Phase 1: Client receives ServerResponseToInitialClientList
	log.Printf("P2P Sync: Waiting for server's file list and requests...")
	serverResponseBytes, err := receivePrefixedMessage(conn, "server response to initial client list")
	if err != nil { log.Printf("P2P Sync: Error receiving server response: %v", err); return }
	var serverResponse ServerResponseToInitialClientList
	if err := json.Unmarshal(serverResponseBytes, &serverResponse); err != nil {
		log.Printf("P2P Sync: Error unmarshalling server response: %v", err); return
	}
	log.Printf("P2P Sync: Received server file list (%d files) and %d file requests.", len(serverResponse.ServerCompleteFileList), len(serverResponse.FilesToRequestFromClient))
	log.Printf("P2P Sync: Server's full file list: %+v", serverResponse.ServerCompleteFileList)

	// Phase 2: Client sends files requested by server
	sizeBuf := make([]byte, 8) // Buffer for sending file size
	for _, fileRelPath := range serverResponse.FilesToRequestFromClient {
		log.Printf("P2P Sync: Server requested '%s'. Preparing to send...", fileRelPath)
		localFilePath := filepath.Join(absPath, fileRelPath)
		file, err := os.Open(localFilePath)
		if err != nil { log.Printf("P2P Sync: Error opening '%s' to send: %v. Skipping.", localFilePath, err); continue }

		fileInfo, err := file.Stat()
		if err != nil { log.Printf("P2P Sync: Error stating '%s': %v. Skipping.", localFilePath, err); file.Close(); continue }
		fileSize := fileInfo.Size()

		binary.BigEndian.PutUint64(sizeBuf, uint64(fileSize))
		log.Printf("P2P Sync: Sending file size %d for '%s'.", fileSize, fileRelPath)
		if _, err := conn.Write(sizeBuf); err != nil {
			log.Printf("P2P Sync: Error sending file size for '%s': %v. Skipping.", fileRelPath, err); file.Close(); continue
		}

		log.Printf("P2P Sync: Sending file content for '%s' (%d bytes)...", fileRelPath, fileSize)
		bytesSent, err := io.Copy(conn, file) // Send content
		file.Close() // Close file after sending content
		if err != nil { log.Printf("P2P Sync: Error sending content for '%s': %v. Skipping.", fileRelPath, err); continue }
		if bytesSent != fileSize { log.Printf("P2P Sync: Mismatch in file send for '%s'. Expected %d, sent %d. Skipping.", fileRelPath, fileSize, bytesSent); continue }

		hash, err := core.GetFileHash(localFilePath)
		if err != nil { log.Printf("P2P Sync: Error calculating hash for '%s': %v. Skipping.", localFilePath, err); continue }
		if err := sendPrefixedMessage(conn, "file hash", []byte(hash)); err != nil {
		    log.Printf("P2P Sync: Error sending hash for '%s': %v. Skipping.", fileRelPath, err); continue
		}
		log.Printf("P2P Sync: Sent file '%s' (size %d, hash %s). Waiting for ACK.", fileRelPath, fileSize, hash)

		ackBytes, err := receivePrefixedMessage(conn, "transfer ACK")
		if err != nil { log.Printf("P2P Sync: Error receiving ACK for '%s': %v. Assuming failure for this file.", fileRelPath, err); continue }
		log.Printf("P2P Sync: Received ACK '%s' for file '%s'", string(ackBytes), fileRelPath)
	}

	// Phase 3: Client determines files it needs and sends request list
	log.Printf("P2P Sync: Determining files to request from server '%s'...", peerInstanceName)
	currentLocalFiles, err := core.ScanFolder(absPath, ".filesync")
	if err != nil { log.Printf("P2P Sync: Error re-scanning local folder: %v", err); return }

	var filesToRequestFromServer []string
	for serverRelPath, serverHash := range serverResponse.ServerCompleteFileList {
		localHash, exists := currentLocalFiles[serverRelPath]
		if !exists {
			filesToRequestFromServer = append(filesToRequestFromServer, serverRelPath)
			log.Printf("P2P Sync: Client will request NEW file '%s' from server.", serverRelPath)
		} else if localHash != serverHash {
			log.Printf("P2P Sync: Conflict for '%s'. Client hash: %s, Server hash: %s. Skipping.", serverRelPath, localHash, serverHash)
		}
	}
	log.Printf("P2P Sync: Requesting %d files from server: %v", len(filesToRequestFromServer), filesToRequestFromServer)
	jsonRequest, err := json.Marshal(filesToRequestFromServer)
	if err != nil { log.Printf("P2P Sync: Error marshalling server file request list: %v", err); return }
	if err := sendPrefixedMessage(conn, "client file request list", jsonRequest); err != nil {
		log.Printf("P2P Sync: Error sending file request list to server: %v", err); return
	}

	// Phase 4: Client receives files requested from server
	for _, fileRelPath := range filesToRequestFromServer {
		log.Printf("P2P Sync: Preparing to receive file '%s' from server...", fileRelPath)

		// Receive file size
		if _, err := io.ReadFull(conn, sizeBuf); err != nil {
			log.Printf("P2P Sync: Error reading file size for '%s' from server: %v. Skipping.", fileRelPath, err); continue;
		}
		fileSize := binary.BigEndian.Uint64(sizeBuf)
		log.Printf("P2P Sync: Expecting file '%s' of size %d bytes from server.", fileRelPath, fileSize)

		localSavePath := filepath.Join(absPath, fileRelPath)
		if err := os.MkdirAll(filepath.Dir(localSavePath), 0755); err != nil {
			log.Printf("P2P Sync: Error creating directory for '%s': %v. Skipping.", localSavePath, err); continue;
		}
		localFile, err := os.Create(localSavePath)
		if err != nil { log.Printf("P2P Sync: Error creating local file '%s': %v. Skipping.", localSavePath, err); continue; }

		bytesRead, err := io.CopyN(localFile, conn, int64(fileSize))
		localFile.Close() // Close file before hashing
		if err != nil { log.Printf("P2P Sync: Error receiving content for '%s': %v. Skipping.", fileRelPath, err); continue; }
		if bytesRead != int64(fileSize) { log.Printf("P2P Sync: Short read for '%s': read %d, expected %d. Skipping.", fileRelPath, bytesRead, fileSize); continue; }
		log.Printf("P2P Sync: File '%s' content received (%d bytes).", fileRelPath, bytesRead)

		// Receive hash
		serverSentHashBytes, err := receivePrefixedMessage(conn, "file hash")
		if err != nil { log.Printf("P2P Sync: Error receiving hash for '%s': %v. Skipping.", fileRelPath, err); continue; }
		serverSentHash := string(serverSentHashBytes)

		localFileHash, err := core.GetFileHash(localSavePath)
		if err != nil { log.Printf("P2P Sync: Error hashing received file '%s': %v", localSavePath, err); /* Potentially send failure ACK */ continue; }

		ackMsg := ""
		if localFileHash == serverSentHash {
			ackMsg = fmt.Sprintf("ACK_TRANSFER_SUCCESS|%s", fileRelPath)
			log.Printf("P2P Sync: File '%s' received from server verification: MATCH", fileRelPath)
		} else {
			ackMsg = fmt.Sprintf("ACK_TRANSFER_FAILURE|%s", fileRelPath)
			log.Printf("P2P Sync: File '%s' received from server verification: MISMATCH. Local: %s, Server: %s", fileRelPath, localFileHash, serverSentHash)
		}
		if err := sendPrefixedMessage(conn, "transfer ACK", []byte(ackMsg)); err != nil {
		    log.Printf("P2P Sync: Error sending ACK for '%s': %v", fileRelPath, err);
		}
	}
	log.Printf("P2P Sync: Sync session with peer %s completed.", peerInstanceName)
}


func handleClientConnection(conn net.Conn, absFolderPath string) {
	remoteAddr := conn.RemoteAddr().String()
	log.Printf("P2P Connection: Client %s TCP connected.", remoteAddr)

	tlsConn, ok := conn.(*tls.Conn)
	if ok {
		state := tlsConn.ConnectionState()
		if state.HandshakeComplete {
			if len(state.PeerCertificates) > 0 {
				peerCert := state.PeerCertificates[0]
				fp := crypto.GetCertFingerprint(peerCert)
				log.Printf("P2P Connection: Client %s connected via TLS. Peer Cert: %s, Fingerprint: %s", remoteAddr, peerCert.Subject, fp)
			} else {
				log.Printf("P2P Connection: Client %s via TLS, no peer certificate.", remoteAddr)
			}
		} else {
			log.Printf("P2P Connection: Client %s TLS handshake NOT complete. Closing.", remoteAddr); conn.Close(); return
		}
	} else {
		log.Printf("P2P Connection: Client %s WITHOUT TLS. Closing.", remoteAddr); conn.Close(); return
	}
	log.Printf("P2P Connection: Syncing folder for %s: %s", remoteAddr, absFolderPath)
	defer func() { log.Printf("P2P Connection: Closing connection with %s.", remoteAddr); conn.Close() }()

	// Phase 1: Server receives client's initial list
	clientListBytes, err := receivePrefixedMessage(conn, "initial client file list")
	if err != nil { log.Printf("P2P Connection: Error receiving client file list from %s: %v", remoteAddr, err); return }
	var clientFiles map[string]string
	if err := json.Unmarshal(clientListBytes, &clientFiles); err != nil {
		log.Printf("P2P Connection: Error unmarshalling client file list from %s: %v", remoteAddr, err); return
	}
	log.Printf("P2P Connection: Received file list (%d files) from peer %s: %+v", len(clientFiles), remoteAddr, clientFiles)

	serverFiles, err := core.ScanFolder(absFolderPath, ".filesync")
	if err != nil { log.Printf("P2P Connection: Error scanning server folder '%s': %v", absFolderPath, err); return }
	log.Printf("P2P Connection: Scanned local folder, %d files found: %+v", len(serverFiles), serverFiles)

	var filesToRequestFromClient []string
	for clientRelPath, clientHash := range clientFiles {
		serverHash, exists := serverFiles[clientRelPath]
		if !exists {
			filesToRequestFromClient = append(filesToRequestFromClient, clientRelPath)
			log.Printf("P2P Connection: Server will request NEW file '%s' from peer %s.", clientRelPath, remoteAddr)
		} else if serverHash != clientHash {
			log.Printf("P2P Connection: Conflict for '%s'. Peer hash: %s, My hash: %s. Skipping.", clientRelPath, clientHash, serverHash)
		}
	}

	serverResponse := ServerResponseToInitialClientList{
		FilesToRequestFromClient: filesToRequestFromClient,
		ServerCompleteFileList:   serverFiles,
	}
	jsonResponse, err := json.Marshal(serverResponse)
	if err != nil { log.Printf("P2P Connection: Error marshalling server response for %s: %v", remoteAddr, err); return }
	if err := sendPrefixedMessage(conn, "server response/requests", jsonResponse); err != nil {
		log.Printf("P2P Connection: Error sending server response to %s: %v", remoteAddr, err); return
	}
	log.Printf("P2P Connection: Sent server file list (%d files) and %d file requests to %s.", len(serverFiles), len(filesToRequestFromClient), remoteAddr)

	// Phase 2: Server receives files it requested
	sizeBuf := make([]byte, 8) // Buffer for receiving file size
	for _, fileRelPath := range filesToRequestFromClient {
		log.Printf("P2P Connection: Receiving requested file '%s' from %s...", fileRelPath, remoteAddr)

		if _, err := io.ReadFull(conn, sizeBuf); err != nil {
			log.Printf("P2P Connection: Error reading file size for '%s' from %s: %v. Skipping.", fileRelPath, remoteAddr, err); continue;
		}
		fileSize := binary.BigEndian.Uint64(sizeBuf)
		log.Printf("P2P Connection: Expecting file '%s' of size %d bytes from %s.", fileRelPath, fileSize, remoteAddr)

		localFilePath := filepath.Join(absFolderPath, fileRelPath)
		if err := os.MkdirAll(filepath.Dir(localFilePath), 0755); err != nil {
			log.Printf("P2P Connection: Error creating directory for '%s': %v. Skipping.", localFilePath, err); continue;
		}
		localFile, err := os.Create(localFilePath)
		if err != nil { log.Printf("P2P Connection: Error creating local file '%s': %v. Skipping.", localFilePath, err); continue; }

		bytesRead, err := io.CopyN(localFile, conn, int64(fileSize))
		localFile.Close() // Close file before hashing
		if err != nil { log.Printf("P2P Connection: Error receiving content for '%s' from %s: %v. Skipping.", fileRelPath, remoteAddr, err); continue; }
		if bytesRead != int64(fileSize) { log.Printf("P2P Connection: Short read for '%s': read %d, expected %d. Skipping.", fileRelPath, bytesRead, fileSize); continue; }
		log.Printf("P2P Connection: File '%s' content received (%d bytes).", fileRelPath, bytesRead)

		clientSentHashBytes, err := receivePrefixedMessage(conn, "file hash")
		if err != nil { log.Printf("P2P Connection: Error receiving hash for '%s' from %s: %v. Skipping.", fileRelPath, remoteAddr, err); continue; }
		clientSentHash := string(clientSentHashBytes)

		receivedFileHash, err := core.GetFileHash(localFilePath)
		if err != nil { log.Printf("P2P Connection: Error hashing received file '%s': %v. Skipping.", localFilePath, err); continue; }

		ackMsg := ""
		if receivedFileHash == clientSentHash {
			ackMsg = fmt.Sprintf("ACK_TRANSFER_SUCCESS|%s", fileRelPath)
			log.Printf("P2P Connection: File '%s' from %s verification: MATCH", fileRelPath, remoteAddr)
		} else {
			ackMsg = fmt.Sprintf("ACK_TRANSFER_FAILURE|%s", fileRelPath)
			log.Printf("P2P Connection: File '%s' from %s verification: MISMATCH. Server: %s, Client: %s", fileRelPath, remoteAddr, receivedFileHash, clientSentHash)
		}
		if err := sendPrefixedMessage(conn, "transfer ACK", []byte(ackMsg)); err != nil {
		    log.Printf("P2P Connection: Error sending ACK for '%s' to %s: %v", fileRelPath, remoteAddr, err);
		}
	}

	// Phase 3: Server waits for client's list of files to pull
	log.Printf("P2P Connection: Waiting for client's list of files to pull from %s...", remoteAddr)
	clientRequestBytes, err := receivePrefixedMessage(conn, "client file request list")
	if err != nil { log.Printf("P2P Connection: Error receiving client file request from %s: %v", remoteAddr, err); return }
	var clientRequestedFiles []string
	if err := json.Unmarshal(clientRequestBytes, &clientRequestedFiles); err != nil {
		log.Printf("P2P Connection: Error unmarshalling client file request from %s: %v", remoteAddr, err); return
	}
	log.Printf("P2P Connection: Peer %s requests %d files: %v", remoteAddr, len(clientRequestedFiles), clientRequestedFiles)

	// Phase 4: Server sends files requested by client
	for _, fileRelPath := range clientRequestedFiles {
		log.Printf("P2P Connection: Sending requested file '%s' to %s...", fileRelPath, remoteAddr)
		localFilePath := filepath.Join(absFolderPath, fileRelPath)
		file, err := os.Open(localFilePath)
		if err != nil { log.Printf("P2P Connection: Error opening '%s' to send to %s: %v. Skipping.", localFilePath, remoteAddr, err); continue; }

		fileInfo, err := file.Stat()
		if err != nil { log.Printf("P2P Connection: Error stating '%s': %v. Skipping.", localFilePath, err); file.Close(); continue; }
		fileSize := fileInfo.Size()

		binary.BigEndian.PutUint64(sizeBuf, uint64(fileSize)) // sizeBuf reused
		log.Printf("P2P Connection: Sending file size %d for '%s'.", fileSize, fileRelPath)
		if _, err := conn.Write(sizeBuf); err != nil {
			log.Printf("P2P Connection: Error sending file size for '%s' to %s: %v. Skipping.", fileRelPath, remoteAddr, err); file.Close(); continue;
		}

		log.Printf("P2P Connection: Sending file content for '%s' (%d bytes)...", fileRelPath, fileSize)
		bytesSent, err := io.Copy(conn, file) // Send content
		file.Close() // Close file after sending content
		if err != nil { log.Printf("P2P Connection: Error sending content for '%s' to %s: %v. Skipping.", fileRelPath, remoteAddr, err); continue; }
		if bytesSent != fileSize { log.Printf("P2P Connection: Mismatch in file send for '%s'. Expected %d, sent %d. Skipping.", fileRelPath, fileSize, bytesSent); continue; }

		hash, err := core.GetFileHash(localFilePath)
		if err != nil { log.Printf("P2P Connection: Error calculating hash for '%s': %v. Skipping.", localFilePath, err); continue; }
		if err := sendPrefixedMessage(conn, "file hash", []byte(hash)); err != nil {
		    log.Printf("P2P Connection: Error sending hash for '%s' to %s: %v. Skipping.", fileRelPath, remoteAddr, err); continue;
		}
		log.Printf("P2P Connection: Sent file '%s' to %s. Waiting for ACK.", fileRelPath, remoteAddr)

		ackBytes, err := receivePrefixedMessage(conn, "transfer ACK")
		if err != nil { log.Printf("P2P Connection: Error receiving ACK for '%s' from %s: %v. Skipping.", fileRelPath, remoteAddr, err); continue; }
		log.Printf("P2P Connection: Received ACK '%s' from %s for file '%s'", string(ackBytes), remoteAddr, fileRelPath)
	}
	log.Printf("P2P Connection: Sync session with %s completed.", remoteAddr)
}


func handleDiscoverCmd() {
	log.Println("Discovering FileSync services (_filesync._tcp)...")
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIPTraffic(zeroconf.IPv4))
	if err != nil {
		log.Fatalf("Failed to initialize resolver (IPv4 attempt): %v", err)
	}

	entries := make(chan *zeroconf.ServiceEntry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func(ctx context.Context, results chan *zeroconf.ServiceEntry) {
		defer close(results)
		err = resolver.Browse(ctx, "_filesync._tcp", "local.", results)
		if err != nil {
			log.Printf("mDNS Browse error: %v", err)
		}
		<-ctx.Done()
	}(ctx, entries)

	log.Println("Browsing for 5 seconds... Press Ctrl+C to stop earlier.")
	foundServices := 0
	for entry := range entries {
		foundServices++
		log.Printf("Discovered Service:")
		log.Printf("  Instance: %s", entry.Instance)
		log.Printf("  Port:     %d", entry.Port)
		log.Printf("  HostName: %s", entry.HostName)
		var ipv4, ipv6 []string
		for _, ip := range entry.AddrIPv4 {
			ipv4 = append(ipv4, ip.String())
		}
		if len(ipv4) > 0 {
			log.Printf("  IPv4:     %s", strings.Join(ipv4, ", "))
		}
		for _, ip := range entry.AddrIPv6 {
			ipv6 = append(ipv6, ip.String())
		}
		if len(ipv6) > 0 {
			log.Printf("  IPv6:     %s", strings.Join(ipv6, ", "))
		}
		log.Printf("  TXT Rec:  %v", entry.Text)
	}

	if foundServices == 0 {
		log.Println("No FileSync services found on the local network.")
	}
	log.Println("Discovery finished.")
}


func handleListenCmd(folderPath string, port int) {
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		log.Printf("Warning: Error resolving absolute path for folder '%s': %v. Using provided path.", folderPath, err)
		absPath = folderPath
	}

	cfg, err := config.LoadConfig(absPath)
	if err != nil {
		log.Fatalf("Critical error loading configuration for listener in '%s': %v. Cannot start listener.", absPath, err)
	}

	configFilePath := filepath.Join(absPath, ".filesync", "config.json")
	_, statErr := os.Stat(configFilePath)

	if cfg.DeviceID == "" {
		log.Fatalf("CRITICAL: DeviceID is empty after LoadConfig in handleListenCmd for folder '%s'. Filesync cannot start.", absPath)
	}

	if os.IsNotExist(statErr) {
		log.Printf("Config file was newly created by LoadConfig (or was empty), saving to persist DeviceID: %s", cfg.DeviceID)
		if err := config.SaveConfig(absPath, cfg); err != nil {
			log.Fatalf("Error saving config after generating DeviceID for folder '%s': %v", absPath, err)
		}
	}

	serverTlsConfig, err := crypto.EnsureTLSConfig(filepath.Join(absPath, ".filesync"), cfg.DeviceID)
	if err != nil {
		log.Fatalf("Error ensuring TLS config for listener in '%s': %v", absPath, err)
	}
	serverTlsConfig.ClientAuth = tls.RequestClientCert


	instanceName := fmt.Sprintf("%s FileSync", cfg.DeviceID)
	if len(cfg.DeviceID) > 20 {
		instanceName = fmt.Sprintf("%.20s...FS", cfg.DeviceID)
	}
	txtRecords := []string{fmt.Sprintf("folder=%s", filepath.Base(absPath))}

	log.Printf("Registering mDNS service: Name='%s', Type='_filesync._tcp', Domain='local.', Port=%d",
		instanceName, port)

	serviceType := "_filesync._tcp"
	domain := "local."
	mdnsService, err := zeroconf.Register(instanceName, serviceType, domain, port, txtRecords, nil)
	if err != nil {
		log.Fatalf("Error registering mDNS service: %v", err)
	}
	defer mdnsService.Shutdown()
	log.Printf("Published mDNS service: Instance='%s', ServiceType='%s', Domain='%s', Port=%d, TXT=%v",
		instanceName, serviceType, domain, port, txtRecords)


	listenAddr := fmt.Sprintf("0.0.0.0:%d", port)
	rawListener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Error starting raw TCP listener on %s for folder '%s': %v", listenAddr, absPath, err)
	}

	tlsListener := tls.NewListener(rawListener, serverTlsConfig)
	log.Printf("TLS Server listening on %s, serving folder '%s'", listenAddr, absPath)
	defer tlsListener.Close()


	for {
		conn, err := tlsListener.Accept()
		if err != nil {
			log.Printf("Error accepting TLS connection: %v. Listener might be closing.", err)
			select {
			default:
				return
			}
		}
		go handleClientConnection(conn, absPath)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Expected 'init', 'add-device', 'sync', 'listen', or 'discover' subcommand")
		printUsage()
		os.Exit(1)
	}

	initCmd := flag.NewFlagSet("init", flag.ExitOnError)
	initFolderFlag := initCmd.String("folder", ".", "Path to the folder to initialize")

	addDeviceCmd := flag.NewFlagSet("add-device", flag.ExitOnError)
	addDeviceFolderFlag := addDeviceCmd.String("folder", ".", "Path to the configuration folder (default: current directory)")
	addDeviceNameFlag := addDeviceCmd.String("name", "", "User-friendly name for the device (required)")
	addDeviceAddressFlag := addDeviceCmd.String("address", "", "IP address or hostname of the device (required)")
	addDevicePortFlag := addDeviceCmd.Int("port", 0, "Port number the device is listening on (required)")
	addDeviceKeyFlag := addDeviceCmd.String("key", "", "Pre-shared key for authentication (required)")

	syncCmd := flag.NewFlagSet("sync", flag.ExitOnError)
	syncFolderFlag := syncCmd.String("folder", ".", "Path to the folder to sync")
	syncPeerFlag := syncCmd.String("peer", "", "Instance name of the peer to sync with (from discover) (required)")

	listenCmd := flag.NewFlagSet("listen", flag.ExitOnError)
	listenFolderFlag := listenCmd.String("folder", ".", "Path to the folder to sync")
	listenPortFlag := listenCmd.Int("port", 61000, "Port to listen on")

	discoverCmd := flag.NewFlagSet("discover", flag.ExitOnError)

	initCmd.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s init:\n", os.Args[0])
		initCmd.PrintDefaults()
	}
	addDeviceCmd.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s add-device:\n", os.Args[0])
		addDeviceCmd.PrintDefaults()
	}
	syncCmd.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s sync:\n", os.Args[0])
		syncCmd.PrintDefaults()
	}
	listenCmd.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s listen:\n", os.Args[0])
		listenCmd.PrintDefaults()
	}
	discoverCmd.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s discover\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "  Discovers other FileSync services on the local network via mDNS.")
	}


	subcommand := os.Args[1]

	switch subcommand {
	case "init":
		initCmd.Parse(os.Args[2:])
		handleInitCmd(*initFolderFlag)
	case "add-device":
		addDeviceCmd.Parse(os.Args[2:])
		if *addDeviceNameFlag == "" || *addDeviceAddressFlag == "" || *addDevicePortFlag == 0 || *addDeviceKeyFlag == "" {
			fmt.Fprintln(os.Stderr, "Error: All flags for add-device (-name, -address, -port, -key) are required.")
			addDeviceCmd.Usage()
			os.Exit(1)
		}
		handleAddDeviceCmd(*addDeviceFolderFlag, *addDeviceNameFlag, *addDeviceAddressFlag, *addDeviceKeyFlag, *addDevicePortFlag)
	case "sync":
		syncCmd.Parse(os.Args[2:])
		if *syncPeerFlag == "" {
			fmt.Fprintln(os.Stderr, "Error: -peer flag is required for sync.")
			syncCmd.Usage()
			os.Exit(1)
		}
		handleSyncCmd(*syncFolderFlag, *syncPeerFlag)
	case "listen":
		listenCmd.Parse(os.Args[2:])
		handleListenCmd(*listenFolderFlag, *listenPortFlag)
	case "discover":
		discoverCmd.Parse(os.Args[2:])
		handleDiscoverCmd()
	default:
		fmt.Printf("Unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s <subcommand> [options]

Subcommands:
  init        Initialize a folder for syncing.
  add-device  Add a new remote device to the configuration.
  sync        Synchronize the folder with a remote device.
  listen      Listen for incoming sync connections.
  discover    Discover other FileSync services on the network.

Use "%s <subcommand> -help" for more information about a subcommand.
`, os.Args[0], os.Args[0])
}
