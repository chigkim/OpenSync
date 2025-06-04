# Lightweight File Sync Tool (Go Version)

This tool provides a lightweight solution for synchronizing files between devices using Go. It's designed to be secure and open-source.

## Features (Planned)

*   Sync a single folder between two devices.
*   Command-line interface.
*   Secure file transfer (TLS).
*   Cross-platform compatibility.
*   Efficient synchronization using file hashes.

## Project Status

Currently under development. The project has been re-initiated in Go.

## Testing

This section guides you through testing the P2P file synchronization between two simulated peers on a local machine.

**Prerequisites:**

*   Go installed (version 1.18+ recommended).
*   A system with mDNS support (e.g., Avahi on Linux, Bonjour on macOS).
*   Firewall configured to allow traffic on the chosen data port (default 61000) and mDNS port (UDP 5353).
*   Two terminal windows (Terminal 1 for Peer A, Terminal 2 for Peer B).

**Steps:**

1.  **Build the Application:**
    In the project's root directory, run:
    ```bash
    go build -o filesync cmd/filesync/main.go
    ```
    This creates the `filesync` executable.

2.  **Set Up Test Directories:**
    Create separate directories for each peer's synchronized folder:
    ```bash
    mkdir /tmp/peerA_sync
    mkdir /tmp/peerB_sync
    ```
    (You can choose any path you prefer.)

3.  **Initialize Sync Folders:**
    *   **Peer A (Terminal 1):**
        ```bash
        ./filesync init -folder /tmp/peerA_sync
        ```
        This creates `.filesync/config.json`, `cert.pem`, and `key.pem` in `/tmp/peerA_sync`.
    *   **Peer B (Terminal 2):**
        ```bash
        ./filesync init -folder /tmp/peerB_sync
        ```
        This does the same for Peer B.

4.  **Start Peer A (Listener):**
    *   **Peer A (Terminal 1):**
        ```bash
        ./filesync listen -folder /tmp/peerA_sync -port 61000
        ```
        Peer A will log that it's listening and publish its service via mDNS (e.g., "Published service: yourhostname-abcdef-FS FileSync _filesync._tcp. local. ..."). **Note down the instance name** (e.g., `yourhostname-abcdef-FS FileSync`).

5.  **Create Test Files:**
    *   **In `/tmp/peerA_sync` (e.g., using a third terminal or before starting `listen` on Peer A):**
        ```bash
        echo "File from Peer A" > /tmp/peerA_sync/file_only_on_A.txt
        mkdir /tmp/peerA_sync/shared_folder
        echo "Original content from A" > /tmp/peerA_sync/shared_folder/conflicting_file.txt
        ```
    *   **In `/tmp/peerB_sync` (e.g., using a third terminal or before running `sync` on Peer B):**
        ```bash
        echo "File from Peer B" > /tmp/peerB_sync/file_only_on_B.txt
        mkdir /tmp/peerB_sync/shared_folder
        echo "Modified content from B" > /tmp/peerB_sync/shared_folder/conflicting_file.txt
        ```

6.  **Run Sync on Peer B (Initiator):**
    *   **Peer B (Terminal 2):**
        Replace `"INSTANCE_NAME_OF_PEER_A"` with the actual instance name you noted from Peer A's logs.
        ```bash
        ./filesync sync -folder /tmp/peerB_sync -peer "INSTANCE_NAME_OF_PEER_A"
        ```

7.  **Verify Results:**
    *   **Logs:**
        *   Both peers should log TLS handshake details and peer certificate fingerprints.
        *   Peer A's logs should show it received `file_only_on_B.txt`.
        *   Peer B's logs should show it received `file_only_on_A.txt`.
        *   Both logs should indicate a conflict for `shared_folder/conflicting_file.txt` and that it was skipped.
    *   **File System:**
        *   `/tmp/peerA_sync` should now contain `file_only_on_A.txt` (original) and `file_only_on_B.txt` (synced). `shared_folder/conflicting_file.txt` should remain "Original content from A".
        *   `/tmp/peerB_sync` should now contain `file_only_on_B.txt` (original) and `file_only_on_A.txt` (synced). `shared_folder/conflicting_file.txt` should remain "Modified content from B".

**Troubleshooting Notes:**

*   **mDNS:** Ensure mDNS/Bonjour/Avahi is running and allowed by your firewall. Discovery can sometimes take a few seconds.
*   **Firewall:** Check for OS firewall rules blocking the data port (e.g., 61000) or mDNS UDP port 5353.
*   **Instance Name:** Ensure you use the exact mDNS instance name advertised by the listener.
*   **Single Sync Pass:** The `sync` command performs one bi-directional synchronization pass and then exits. The `listen` command remains active.
