# Reticulum CLI Utilities

Go Reticulum provides standard command-line tools for node administration, diagnostics, file transfers, and remote access across the mesh.

---

## gornstatus

Displays the status of local Reticulum network interfaces, traffic volumes, and active routing table entries.

```bash
gornstatus
```

Options:
- `-a, --all`: Show detailed interface parameters (frequencies, spreading factors, MTUs).
- `-r, --routes`: Display complete routing table with known destination hashes and next hops.
- `-j, --json`: Emit metrics in machine-readable JSON format.

---

## gornpath

Discovers and prints the best known route and hop count to a given destination hash.

```bash
gornpath <destination_hash>
```

Options:
- `-d, --drop`: Drop any cached path to the destination and force fresh route discovery.
- `-w, --wait <sec>`: Maximum time to wait for path discovery announces.

---

## gornprobe

Tests reachability and measures round-trip latency to a destination hash using an encrypted Reticulum probe packet.

```bash
gornprobe <destination_hash>
```

---

## gornid

Manages 64-byte Reticulum cryptographic identities (Ed25519 / X25519 keypairs).

```bash
# Generate a new identity file
gornid -g ~/.reticulum/my_identity

# Print the identity hash from a private key file
gornid -i ~/.reticulum/my_identity

# Print the destination address for a specific app name
gornid -i ~/.reticulum/my_identity -a "lxmf.delivery"
```

---

## gorncp

Secure, authenticated file copy across Reticulum links.

```bash
# Send a file to a remote destination
gorncp file.tar.gz <destination_hash>

# Receive files on a local identity
gorncp -l -i ~/.reticulum/my_identity
```

---

## gornsh

Secure, encrypted remote shell over Reticulum.

```bash
# Connect to an authorized remote server
gornsh <destination_hash>

# Run a listener to accept authorized inbound shell sessions
gornsh -l -i ~/.reticulum/my_identity -a ~/.reticulum/authorized_keys
```

---

## gorngit

Enables Git operations (`git clone`, `git push`, `git pull`) over the Reticulum mesh via the `gogit-remote-rns` helper.

```bash
git clone rns://<destination_hash>/repo.git
```

---

## gornpkg

Offline package manager and distribution utility for sharing software archives, packages, and firmware bundles across Reticulum mesh networks.

```bash
# Publish a package bundle
gornpkg publish my-package.bundle

# Fetch and install a package bundle
gornpkg get <destination_hash>/my-package.bundle
```

