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

---

## gornsd

The core Reticulum Network Stack transport daemon (Go port of `rnsd`). It manages physical network interfaces, packet routing, and path tables, and provides a shared instance IPC socket (`@rns/default`) so that multiple local Reticulum applications (`golxmd`, `gorrcd`, `gorrcbot`, `gonomadnet`) can coexist without port or interface contention.

```bash
# Run in foreground (desktop testing)
gornsd -v

# Run as background service
gornsd -s

# Print default example configuration
gornsd --exampleconfig > ~/.reticulum/config
```

Options:
- `-s, --service`: Run as background service.
- `--config <dir>`: Path to alternative Reticulum config directory (default: `~/.reticulum`).
- `--exampleconfig`: Print annotated example configuration to stdout and exit.
- `-v, --verbose`: Increase verbosity.
- `-q, --quiet`: Suppress non-error output.

---

## gornx

Remote command execution over Reticulum (Go port of `rnx`). Supports authenticated, encrypted remote execution in listen or execution mode.

```bash
# Listen mode: accept authorized remote execution requests
gornx -l -i ~/.reticulum/my_identity

# Execute mode: run command on remote destination
gornx <destination_hash> "uptime" -i ~/.reticulum/my_identity
```

Options:
- `-l`: Run in listen mode to accept authorized remote commands.
- `-i <path>`: Path to identity key file.
- `--config <dir>`: Path to alternative Reticulum config directory.
- `-v, --verbose`: Verbose output.
- `-q, --quiet`: Quiet output.

Authorized caller identity hashes are configured in `~/.rnx/allowed_identities` or `/etc/rnx/allowed_identities`.

---

## gornir

Distributed Identity Resolver daemon (Go port of `rnir`). Discovers and resolves Reticulum destination addresses and identities across the mesh.

```bash
gornir
```

Options:
- `--config <dir>`: Path to Reticulum config directory.
- `--exampleconfig`: Print annotated example configuration.

---

## gorngcs

Git commit signature signer and validator (Go port of `rngcs` from `RNS/Utilities/rngit/commitsigs.py`). Produces and verifies SSHSIG-format signatures backed by Reticulum Signed Git (RSG) cryptographic identity envelopes.

```bash
# Sign a commit or file
gorngcs -Y sign -f ~/.reticulum/my_identity -n git commit-message.txt

# Verify a signature
gorngcs -Y verify -s commit-message.txt.sig -f ~/.reticulum/allowed_signers
```

Options:
- `-Y sign`: Generate commit signature.
- `-Y verify`: Verify signature against allowed signers.
- `-Y find-principals`: List principal identities in signature.
- `-Y check-novalidate`: Check signature structure without cryptographic validation.
- `-f <file>`: Key file (signing) or allowed signers file (verifying).
- `-s <file>`: Signature file to verify.
- `-n <namespace>`: Verification namespace (e.g. `git`).

