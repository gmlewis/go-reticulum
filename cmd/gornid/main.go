// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornid is a Reticulum-based identity management and cryptographic utility.
//
// It provides features for:
//   - Generating new Reticulum identities.
//   - Importing and exporting identities in various formats (hex, base32, base64).
//   - Encrypting and decrypting files for specific identities.
//   - Signing files and validating signatures.
//   - Calculating destination hashes for different app aspects.
//   - Announcing destinations to the network.
//
// Usage:
//
//	Generate a new identity:
//	  gornid -g <path_to_save_identity>
//
//	Display identity information:
//	  gornid -i <identity_hex_or_path> -p [-P]
//
//	Encrypt a file for an identity:
//	  gornid -i <destination_hash_or_path> -e <file_path> [-w <output_path>]
//
//	Sign a file with an identity:
//	  gornid -i <identity_path> -s <file_path> [-w <output_path>]
//
// Flags:
//
//	-config string
//	      path to alternative Reticulum config directory
//	-i string
//	      hexadecimal Reticulum identity or destination hash, or path to Identity file
//	-g string
//	      generate a new Identity at the specified path
//	-m string
//	      import Reticulum identity in hex, base32 or base64 format
//	-x    export identity to hex, base32 or base64 format
//	-p    print identity info and exit
//	-P    allow displaying private keys when printing
//	-e string
//	      encrypt the specified file
//	-d string
//	      decrypt the specified file
//	-s string
//	      sign the specified file
//	-V string
//	      validate the specified signature file against the input file
//	-r string
//	      input file path for operations
//	-w string
//	      output file path for operations
//	-f    force write output even if it overwrites existing files
//	-a string
//	      announce a destination based on this Identity (format: app_name.aspect1.aspect2)
//	-H string
//	      show destination hashes for other aspects for this Identity
//	-b    use base64-encoded input and output
//	-B    use base32-encoded input and output
//	-v    increase verbosity
//	-q    decrease verbosity
package main

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// AppName is the identifier used when creating or loading the default identity.
const AppName = "rnid"

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	identityPath := flag.String("i", "", "hexadecimal Reticulum identity or destination hash, or path to Identity file")
	generatePath := flag.String("g", "", "generate a new Identity")
	importStr := flag.String("m", "", "import Reticulum identity in hex, base32 or base64 format")
	export := flag.Bool("x", false, "export identity to hex, base32 or base64 format")
	verbose := flag.Bool("v", false, "increase verbosity")
	quiet := flag.Bool("q", false, "decrease verbosity")
	announce := flag.String("a", "", "announce a destination based on this Identity")
	hashAspects := flag.String("H", "", "show destination hashes for other aspects for this Identity")
	encryptFile := flag.String("e", "", "encrypt file")
	decryptFile := flag.String("d", "", "decrypt file")
	signFile := flag.String("s", "", "sign file")
	validateFile := flag.String("V", "", "validate signature")
	readFile := flag.String("r", "", "input file path")
	writeFile := flag.String("w", "", "output file path")
	force := flag.Bool("f", false, "write output even if it overwrites existing files")
	requestID := flag.Bool("R", false, "request unknown Identities from the network")
	timeout := flag.Float64("t", 15.0, "identity request timeout before giving up")
	printIdentity := flag.Bool("p", false, "print identity info and exit")
	printPrivate := flag.Bool("P", false, "allow displaying private keys")
	useBase64 := flag.Bool("b", false, "Use base64-encoded input and output")
	useBase32 := flag.Bool("B", false, "Use base32-encoded input and output")
	version := flag.Bool("version", false, "show version and exit")

	log.SetFlags(0)
	flag.Parse()

	if *version {
		fmt.Printf("gornid %v\n", rns.VERSION)
		return
	}

	if *verbose {
		rns.SetLogLevel(rns.LogVerbose)
	}
	if *quiet {
		rns.SetLogLevel(rns.LogWarning)
	}

	if *importStr != "" {
		doImport(*importStr, *useBase64, *useBase32, *printPrivate, *writeFile, *force)
		return
	}

	if *generatePath != "" {
		doGenerate(*generatePath, *force)
		return
	}

	if *identityPath == "" && !*printIdentity && *encryptFile == "" && *decryptFile == "" && *signFile == "" && *validateFile == "" {
		fmt.Println("No identity provided, cannot continue")
		flag.Usage()
		os.Exit(2)
	}

	reticulum, err := rns.NewReticulum(*configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	_ = reticulum
	rns.CompactLogFmt = true

	id := loadIdentity(*identityPath, *requestID, *timeout)
	if id == nil {
		log.Fatal("Could not load or recall identity")
	}

	if *printIdentity {
		doPrintIdentity(id, *useBase64, *useBase32, *printPrivate)
	}

	if *export {
		doExport(id, *useBase64, *useBase32)
	}

	if *hashAspects != "" {
		doHash(id, *hashAspects)
	}

	if *announce != "" {
		doAnnounce(id, *announce)
	}

	if *encryptFile != "" || *decryptFile != "" || *signFile != "" || *validateFile != "" {
		// Handle file operations
		input := *readFile
		if input == "" {
			if *encryptFile != "" {
				input = *encryptFile
			} else if *decryptFile != "" {
				input = *decryptFile
			} else if *signFile != "" {
				input = *signFile
			}
		}
		output := *writeFile
		doFileOp(id, input, output, *encryptFile != "", *decryptFile != "", *signFile != "", *validateFile, *force)
	}
}

func doImport(data string, b64, b32, prv bool, writePath string, force bool) {
	var idBytes []byte
	var err error
	if b64 {
		idBytes, err = base64.RawURLEncoding.DecodeString(data)
	} else if b32 {
		idBytes, err = base32.StdEncoding.DecodeString(data)
	} else {
		idBytes, err = hex.DecodeString(data)
	}

	if err != nil {
		fmt.Printf("Invalid identity data: %v\n", err)
		os.Exit(41)
	}

	id, err := rns.NewIdentity(false)
	if err != nil {
		fmt.Printf("Could not create identity: %v\n", err)
		os.Exit(42)
	}
	if err := id.LoadPrivateKey(idBytes); err != nil {
		// Try loading as public key if private fails
		if err := id.LoadPublicKey(idBytes); err != nil {
			fmt.Printf("Could not load identity: %v\n", err)
			os.Exit(42)
		}
	}

	rns.Log("Identity imported", rns.LogNotice, false)
	doPrintIdentity(id, b64, b32, prv)

	if writePath != "" {
		if _, err := os.Stat(writePath); err == nil && !force {
			fmt.Printf("File %v already exists, not overwriting\n", writePath)
			os.Exit(43)
		}
		if err := id.ToFile(writePath); err != nil {
			fmt.Printf("Error writing identity: %v\n", err)
			os.Exit(44)
		}
		rns.Log(fmt.Sprintf("Wrote imported identity to %v", writePath), rns.LogNotice, false)
	}
}

func doGenerate(path string, force bool) {
	if _, err := os.Stat(path); err == nil && !force {
		rns.Log(fmt.Sprintf("Identity file %v already exists. Not overwriting.", path), rns.LogError, false)
		os.Exit(3)
	}
	id, err := rns.NewIdentity(true)
	if err != nil {
		rns.Log(fmt.Sprintf("Error generating identity: %v", err), rns.LogError, false)
		os.Exit(4)
	}
	if err := id.ToFile(path); err != nil {
		rns.Log(fmt.Sprintf("Error saving identity: %v", err), rns.LogError, false)
		os.Exit(4)
	}
	rns.Log(fmt.Sprintf("New identity <%v> written to %v", id.HexHash, path), rns.LogNotice, false)
}

func loadIdentity(path string, request bool, timeout float64) *rns.Identity {
	if path == "" {
		return nil
	}

	// Try as file first
	if _, err := os.Stat(path); err == nil {
		id, err := rns.FromFile(path)
		if err == nil {
			rns.Logf("Loaded Identity <%v> from %v", rns.LogNotice, false, id.HexHash, path)
			return id
		}
	}

	// Try as hex hash
	hash, err := hex.DecodeString(path)
	if err == nil && len(hash) == rns.TruncatedHashLength/8 {
		id := rns.RecallIdentity(hash)
		if id == nil && request {
			rns.Log(fmt.Sprintf("Requesting unknown Identity for %v...", path), rns.LogNotice, false)
			if err := rns.Transport.RequestPath(hash); err != nil {
				rns.Logf("Identity request failed for %v: %v", rns.LogError, false, path, err)
				return nil
			}
			deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
			for time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				id = rns.RecallIdentity(hash)
				if id != nil {
					return id
				}
			}
			rns.Log("Identity request timed out", rns.LogNotice, false)
		}
		return id
	}

	return nil
}

func doPrintIdentity(id *rns.Identity, b64, b32, prv bool) {
	pub := id.GetPublicKey()
	var pubStr string
	if b64 {
		pubStr = base64.RawURLEncoding.EncodeToString(pub)
	} else if b32 {
		pubStr = base32.StdEncoding.EncodeToString(pub)
	} else {
		pubStr = hex.EncodeToString(pub)
	}
	rns.Logf("Public Key  : %v", rns.LogNotice, false, pubStr)

	if prv {
		priv := id.GetPrivateKey()
		if priv != nil {
			var privStr string
			if b64 {
				privStr = base64.RawURLEncoding.EncodeToString(priv)
			} else if b32 {
				privStr = base32.StdEncoding.EncodeToString(priv)
			} else {
				privStr = hex.EncodeToString(priv)
			}
			rns.Logf("Private Key : %v", rns.LogNotice, false, privStr)
		} else {
			rns.Log("Private Key : Hidden", rns.LogNotice, false)
		}
	} else {
		rns.Log("Private Key : Hidden", rns.LogNotice, false)
	}
}

func doExport(id *rns.Identity, b64, b32 bool) {
	priv := id.GetPrivateKey()
	if priv == nil {
		rns.Log("Identity doesn't hold a private key, cannot export", rns.LogNotice, false)
		os.Exit(50)
	}
	var privStr string
	if b64 {
		privStr = base64.RawURLEncoding.EncodeToString(priv)
	} else if b32 {
		privStr = base32.StdEncoding.EncodeToString(priv)
	} else {
		privStr = hex.EncodeToString(priv)
	}
	rns.Log(fmt.Sprintf("Exported Identity : %v", privStr), rns.LogNotice, false)
}

func doHash(id *rns.Identity, aspects string) {
	parts := strings.Split(aspects, ".")
	if len(parts) == 0 {
		rns.Log("Invalid destination aspects specified", rns.LogError, false)
		os.Exit(32)
	}
	appName := parts[0]
	var subAspects []string
	if len(parts) > 1 {
		subAspects = parts[1:]
	}

	dest, err := rns.NewDestination(id, rns.DestinationOut, rns.DestinationSingle, appName, subAspects...)
	if err != nil {
		rns.Logf("Error calculating hash: %v", rns.LogNotice, false, err)
		os.Exit(32)
	}
	rns.Logf("The %v destination for this Identity is <%x>", rns.LogNotice, false, aspects, dest.Hash)
	rns.Logf("The full destination specifier is %v", rns.LogNotice, false, dest)
}

func doAnnounce(id *rns.Identity, aspects string) {
	parts := strings.Split(aspects, ".")
	if len(parts) < 2 {
		rns.Log("Invalid destination aspects specified, at least app_name and one aspect required for announcement", rns.LogError, false)
		os.Exit(32)
	}
	appName := parts[0]
	subAspects := parts[1:]

	dest, err := rns.NewDestination(id, rns.DestinationIn, rns.DestinationSingle, appName, subAspects...)
	if err != nil {
		rns.Log(fmt.Sprintf("Error creating destination: %v", err), rns.LogNotice, false)
		os.Exit(32)
	}

	rns.Log(fmt.Sprintf("Created destination %v", dest), rns.LogNotice, false)
	rns.Log(fmt.Sprintf("Announcing destination %x", dest.Hash), rns.LogNotice, false)
	if err := dest.Announce(nil); err != nil {
		rns.Log(fmt.Sprintf("Error sending announce: %v", err), rns.LogError, false)
		os.Exit(32)
	}
}

func doFileOp(id *rns.Identity, inputPath, outputPath string, enc, dec, sign bool, valPath string, force bool) {
	if enc {
		if outputPath == "" {
			outputPath = inputPath + ".rfe"
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			rns.Log(fmt.Sprintf("Error reading file: %v", err), rns.LogNotice, false)
			os.Exit(12)
		}
		ciphertext, err := id.Encrypt(data, nil)
		if err != nil {
			rns.Log(fmt.Sprintf("Error encrypting: %v", err), rns.LogNotice, false)
			os.Exit(26)
		}
		if _, err := os.Stat(outputPath); err == nil && !force {
			rns.Log(fmt.Sprintf("Output file %v already exists, not overwriting", outputPath), rns.LogNotice, false)
			os.Exit(15)
		}
		if err := os.WriteFile(outputPath, ciphertext, 0644); err != nil {
			rns.Log(fmt.Sprintf("Error writing file: %v", err), rns.LogNotice, false)
			os.Exit(15)
		}
		rns.Log(fmt.Sprintf("File %v encrypted for %v to %v", inputPath, id.HexHash, outputPath), rns.LogNotice, false)
	} else if dec {
		if outputPath == "" && strings.HasSuffix(inputPath, ".rfe") {
			outputPath = strings.TrimSuffix(inputPath, ".rfe")
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			rns.Log(fmt.Sprintf("Error reading file: %v", err), rns.LogNotice, false)
			os.Exit(12)
		}
		plaintext, err := id.Decrypt(data, nil, false)
		if err != nil {
			rns.Log(fmt.Sprintf("Error decrypting: %v", err), rns.LogNotice, false)
			os.Exit(31)
		}
		if _, err := os.Stat(outputPath); err == nil && !force {
			rns.Log(fmt.Sprintf("Output file %v already exists, not overwriting", outputPath), rns.LogNotice, false)
			os.Exit(15)
		}
		if err := os.WriteFile(outputPath, plaintext, 0644); err != nil {
			rns.Log(fmt.Sprintf("Error writing file: %v", err), rns.LogNotice, false)
			os.Exit(15)
		}
		rns.Log(fmt.Sprintf("File %v decrypted with %v to %v", inputPath, id.HexHash, outputPath), rns.LogNotice, false)
	} else if sign {
		if outputPath == "" {
			outputPath = inputPath + ".rsg"
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			rns.Log(fmt.Sprintf("Error reading file: %v", err), rns.LogNotice, false)
			os.Exit(12)
		}
		signature, err := id.Sign(data)
		if err != nil {
			rns.Log(fmt.Sprintf("Error signing: %v", err), rns.LogNotice, false)
			os.Exit(19)
		}
		if _, err := os.Stat(outputPath); err == nil && !force {
			rns.Log(fmt.Sprintf("Output file %v already exists, not overwriting", outputPath), rns.LogNotice, false)
			os.Exit(15)
		}
		if err := os.WriteFile(outputPath, signature, 0644); err != nil {
			rns.Log(fmt.Sprintf("Error writing file: %v", err), rns.LogNotice, false)
			os.Exit(15)
		}
		rns.Log(fmt.Sprintf("File %v signed with %v to %v", inputPath, id.HexHash, outputPath), rns.LogNotice, false)
	} else if valPath != "" {
		sig, err := os.ReadFile(valPath)
		if err != nil {
			rns.Log(fmt.Sprintf("Error reading signature file: %v", err), rns.LogNotice, false)
			os.Exit(21)
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			rns.Log(fmt.Sprintf("Error reading data file: %v", err), rns.LogNotice, false)
			os.Exit(12)
		}
		if id.Verify(sig, data) {
			rns.Log(fmt.Sprintf("Signature %v for file %v made by Identity %v is valid", valPath, inputPath, id.HexHash), rns.LogNotice, false)
		} else {
			rns.Log(fmt.Sprintf("Signature %v for file %v is invalid", valPath, inputPath), rns.LogNotice, false)
			os.Exit(22)
		}
	}
}
