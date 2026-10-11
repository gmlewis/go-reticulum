# Reticulum CLI Utilities

Go Reticulum provides standard command-line tools for node administration, diagnostics, file transfers, and remote access across the mesh.

---

## gornstatus

Displays the status of local Reticulum network interfaces, traffic volumes, and active routing table entries.

```bash
gornstatus
```

Options:
- `-a, --all`: Include interfaces that are currently down.
- `-m, --monitor`: Keep monitoring, instead of printing once and exiting.
- `-s, --sort <key>`: Sort interfaces by `rate`, `traffic`, `rx`, `tx`, `announces`, `held`, `gravity` and others.
- `-r, --reverse`: Reverse the sort order.
- `-t, --totals`: Show traffic totals.
- `-j, --json`: Emit metrics in machine-readable JSON format.

---

## gornpath

Discovers and prints the best known route and hop count to a given destination hash.

```bash
gornpath <destination_hash>
```

Options:
- `-t, --table`: Show every known path — the routing table.
- `-m, --max <hops>`: Filter the path table by hop count.
- `-r, --rates`: Show announce rate information.
- `-d, --drop`: Drop the path to a destination and force fresh discovery.

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

# Run a listener that allows one caller, by identity hash
gornsh -l -i ~/.reticulum/my_identity --allowed <identity_hash>
```

Allowed caller hashes can also be listed one per line, in
`~/.rnsh/allowed_identities` or `~/.config/rnsh/allowed_identities`.

---

## gorngit

Enables Git operations (`git clone`, `git push`, `git pull`) over the Reticulum mesh via the `gogit-remote-rns` helper.

```bash
git clone rns://<destination_hash>/repo.git
```

---

## gornpkg

The Go port of the Reticulum Meta Package Manager. **It is a stub:** it
initializes the Reticulum stack to show the port starts, then exits. There is no
publish or fetch yet.

```bash
gornpkg --version
gornpkg --exampleconfig
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

The Go port of the Reticulum Distributed Identity Resolver. **It is a stub:** it
initializes the Reticulum stack and exits, so nothing is resolved yet.

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

