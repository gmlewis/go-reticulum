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
package main

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/utils"
)

type appT struct {
	configDir     string
	identityPath  string
	generatePath  string
	importStr     string
	logger        *rns.Logger
	export        bool
	verbose       counter
	quiet         counter
	announce      string
	hashAspects   string
	encryptFile   string
	decryptFile   string
	signFile      string
	validateFile  string
	readFile      string
	writeFile     string
	force         bool
	requestID     bool
	timeout       float64
	printIdentity bool
	printPrivate  bool
	useBase64     bool
	useBase32     bool
	useStdin      bool
	useStdout     bool
	version       bool
}

func logMessage(logger *rns.Logger, msg string, level int, pt bool) {
	logger.Log(msg, level, pt)
}

func logf(logger *rns.Logger, format string, level int, pt bool, args ...any) {
	logger.Log(fmt.Sprintf(format, args...), level, pt)
}

func (a *appT) getLogger() *rns.Logger {
	if a != nil && a.logger != nil {
		return a.logger
	}
	return rns.NewLogger()
}

func (a *appT) run() {
	a.logger = rns.NewLogger()
	logger := a.logger
	var ops int
	for _, op := range []bool{a.encryptFile != "", a.decryptFile != "", a.validateFile != "", a.signFile != ""} {
		if op {
			ops++
		}
	}

	if ops > 1 {
		logMessage(logger, "This utility currently only supports one of the encrypt, decrypt, sign or verify operations per invocation", rns.LogError, false)
		os.Exit(1)
	}

	if a.version {
		utils.PrintVersion(os.Stdout, "gornid", rns.VERSION)
		return
	}

	if a.verbose != 0 || a.quiet != 0 {
		logger.SetLogLevel(4 + int(a.verbose) - int(a.quiet))
	}

	if a.readFile == "" {
		if a.encryptFile != "" {
			a.readFile = a.encryptFile
		}
		if a.decryptFile != "" {
			a.readFile = a.decryptFile
		}
		if a.signFile != "" {
			a.readFile = a.signFile
		}
		if a.validateFile != "" && strings.HasSuffix(strings.ToLower(a.validateFile), ".rsg") {
			a.readFile = strings.Replace(a.validateFile, ".rsg", "", 1)
		}
	}

	if a.importStr != "" {
		a.doImport(a.importStr, a.useBase64, a.useBase32, a.printPrivate, a.writeFile, a.force)
		return
	}

	if a.generatePath == "" && a.identityPath == "" {
		_, _ = fmt.Fprint(os.Stderr, "\nNo identity provided, cannot continue\n")
		a.usage(os.Stderr)
		os.Exit(2)
	}

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulumWithLogger(ts, a.configDir, logger)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Log(fmt.Sprintf("Warning: Could not close Reticulum properly: %v", err), rns.LogWarning, false)
		}
	}()

	logger.SetCompactLogFmt(true)
	if a.useStdout {
		logger.SetLogLevel(-1)
	}

	if a.generatePath != "" {
		a.doGenerate(a.generatePath, a.force)
		return
	}

	id := a.loadIdentity(ret.Transport(), a.identityPath, a.requestID, a.timeout)
	if id == nil {
		log.Fatal("Could not load or recall identity")
	}

	if a.printIdentity {
		a.doPrintIdentity(id, a.useBase64, a.useBase32, a.printPrivate)
		os.Exit(0)
	}

	if a.export {
		a.doExport(id, a.useBase64, a.useBase32)
		os.Exit(0)
	}

	if a.hashAspects != "" {
		a.doHash(ts, id, a.hashAspects)
	}

	if a.announce != "" {
		a.doAnnounce(ts, id, a.announce)
	}

	if a.encryptFile != "" || a.decryptFile != "" || a.signFile != "" || a.validateFile != "" {
		a.doFileOps(id, a.readFile, a.writeFile, a.encryptFile, a.decryptFile, a.signFile, a.validateFile, a.force, a.useStdout)
	}
}

func main() {
	log.SetFlags(0)
	app, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if err == utils.ErrHelp {
			return
		}
		log.Fatal(err)
	}
	app.run()
}

func (a *appT) doImport(data string, b64, b32, prv bool, writePath string, force bool) {
	logger := a.getLogger()
	var idBytes []byte
	var err error
	if b64 {
		idBytes, err = base64.URLEncoding.DecodeString(data)
	} else if b32 {
		idBytes, err = base32.StdEncoding.DecodeString(data)
	} else {
		idBytes, err = hex.DecodeString(data)
	}

	if err != nil {
		fmt.Printf("Invalid identity data specified for import: %v\n", err)
		os.Exit(41)
	}

	id, err := rns.FromBytes(idBytes)
	if err != nil {
		fmt.Printf("Could not create Reticulum identity from specified data: %v\n", err)
		os.Exit(42)
	}

	logMessage(logger, "Identity imported", rns.LogNotice, false)
	a.doPrintIdentity(id, b64, b32, prv)

	if writePath != "" {
		wp := expandUser(writePath)
		if !force {
			if _, err := os.Stat(wp); err == nil {
				fmt.Printf("File %v already exists, not overwriting\n", wp)
				os.Exit(43)
			}
		}
		if err := id.ToFile(wp); err != nil {
			fmt.Printf("Error while writing imported identity to file: %v\n", err)
			os.Exit(44)
		}
		logf(logger, "Wrote imported identity to %v", rns.LogNotice, false, writePath)
	}
}

func (a *appT) doGenerate(path string, force bool) {
	logger := a.getLogger()
	if _, err := os.Stat(path); err == nil && !force {
		logMessage(logger, fmt.Sprintf("Identity file %v already exists. Not overwriting.", path), rns.LogError, false)
		os.Exit(3)
	}
	id, err := rns.NewIdentity(true)
	if err != nil {
		logMessage(logger, "An error ocurred while saving the generated Identity.", rns.LogError, false)
		logf(logger, "The contained exception was: %v", rns.LogError, false, err)
		os.Exit(4)
	}
	if err := id.ToFile(path); err != nil {
		logMessage(logger, "An error ocurred while saving the generated Identity.", rns.LogError, false)
		logf(logger, "The contained exception was: %v", rns.LogError, false, err)
		os.Exit(4)
	}
	logf(logger, "New identity %v written to %v", rns.LogNotice, false, rns.PrettyHexFromString(id.HexHash), path)
}

func loadIdentity(ts rns.Transport, path string, request bool, timeout float64) *rns.Identity {
	return (&appT{logger: rns.NewLogger()}).loadIdentity(ts, path, request, timeout)
}

func (a *appT) loadIdentity(ts rns.Transport, path string, request bool, timeout float64) *rns.Identity {
	logger := a.getLogger()
	if path == "" {
		return nil
	}

	hashStrLen := rns.TruncatedHashLength / 8 * 2
	_, fileErr := os.Stat(path)
	isFile := fileErr == nil

	if len(path) == hashStrLen && !isFile {
		hash, err := hex.DecodeString(path)
		if err != nil {
			logMessage(logger, "Invalid hexadecimal hash provided", rns.LogError, false)
			os.Exit(7)
		}

		id := ts.Recall(hash)
		// TODO:
		// if id == nil {
		//   Try as identity hash if not found as destination hash
		//   (Note: Transport.Recall currently only checks destination hashes
		//   but we might need a way to recall by identity hash too if needed)
		//   For now, gornid uses destination hashes by default.
		// }

		if id == nil {
			if !request {
				logf(logger, "Could not recall Identity for %v.", rns.LogError, false, rns.PrettyHex(hash))
				logMessage(logger, "You can query the network for unknown Identities with the -R option.", rns.LogError, false)
				os.Exit(5)
			}
			if err := ts.RequestPath(hash); err != nil {
				logf(logger, "Identity request failed for %v: %v", rns.LogError, false, rns.PrettyHex(hash), err)
				os.Exit(6)
			}
			deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
			for time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				id = ts.Recall(hash)
				if id != nil {
					logf(logger, "Received Identity %v for destination %v from the network", rns.LogNotice, false, rns.PrettyHexFromString(id.HexHash), rns.PrettyHex(hash))
					return id
				}
			}
			logMessage(logger, "Identity request timed out", rns.LogError, false)
			os.Exit(6)
		}

		identStr := rns.PrettyHexFromString(id.HexHash)
		hashStr := rns.PrettyHex(hash)
		if identStr == hashStr {
			logf(logger, "Recalled Identity %v", rns.LogNotice, false, identStr)
		} else {
			logf(logger, "Recalled Identity %v for destination %v", rns.LogNotice, false, identStr, hashStr)
		}
		return id
	}

	if !isFile {
		logMessage(logger, "Specified Identity file not found", rns.LogNotice, false)
		os.Exit(8)
	}
	id, err := rns.FromFile(path)
	if err != nil {
		logMessage(logger, "Could not decode Identity from specified file", rns.LogNotice, false)
		os.Exit(9)
	}
	logf(logger, "Loaded Identity %v from %v", rns.LogNotice, false, rns.PrettyHexFromString(id.HexHash), path)
	return id
}

func (a *appT) doPrintIdentity(id *rns.Identity, b64, b32, prv bool) {
	logger := a.getLogger()
	pub := id.GetPublicKey()
	var pubStr string
	if b64 {
		pubStr = base64.URLEncoding.EncodeToString(pub)
	} else if b32 {
		pubStr = base32.StdEncoding.EncodeToString(pub)
	} else {
		pubStr = hex.EncodeToString(pub)
	}
	logf(logger, "Public Key  : %v", rns.LogNotice, false, pubStr)

	privKey := id.GetPrivateKey()
	if privKey != nil {
		if prv {
			var privStr string
			if b64 {
				privStr = base64.URLEncoding.EncodeToString(privKey)
			} else if b32 {
				privStr = base32.StdEncoding.EncodeToString(privKey)
			} else {
				privStr = hex.EncodeToString(privKey)
			}
			logf(logger, "Private Key : %v", rns.LogNotice, false, privStr)
		} else {
			logMessage(logger, "Private Key : Hidden", rns.LogNotice, false)
		}
	}
}

func (a *appT) doExport(id *rns.Identity, b64, b32 bool) {
	logger := a.getLogger()
	priv := id.GetPrivateKey()
	if priv == nil {
		logMessage(logger, "Identity doesn't hold a private key, cannot export", rns.LogNotice, false)
		os.Exit(50)
	}
	var privStr string
	if b64 {
		privStr = base64.URLEncoding.EncodeToString(priv)
	} else if b32 {
		privStr = base32.StdEncoding.EncodeToString(priv)
	} else {
		privStr = hex.EncodeToString(priv)
	}
	logMessage(logger, fmt.Sprintf("Exported Identity : %v", privStr), rns.LogNotice, false)
}

func (a *appT) doHash(ts rns.Transport, id *rns.Identity, aspects string) {
	logger := a.getLogger()
	parts := strings.Split(aspects, ".")
	if len(parts) == 0 {
		logMessage(logger, "Invalid destination aspects specified", rns.LogError, false)
		os.Exit(32)
	}
	appName := parts[0]
	var subAspects []string
	if len(parts) > 1 {
		subAspects = parts[1:]
	}

	if id.GetPublicKey() == nil {
		logMessage(logger, "An error ocurred while attempting to send the announce.", rns.LogError, false)
		logMessage(logger, "The contained exception was: No public key known", rns.LogError, false)
		os.Exit(0)
	}
	dest, err := rns.NewDestination(ts, id, rns.DestinationOut, rns.DestinationSingle, appName, subAspects...)
	if err != nil {
		logMessage(logger, "An error ocurred while attempting to send the announce.", rns.LogError, false)
		logf(logger, "The contained exception was: %v", rns.LogError, false, err)
		os.Exit(0)
	}
	logf(logger, "The %v destination for this Identity is %v", rns.LogNotice, false, aspects, rns.PrettyHex(dest.Hash))
	logf(logger, "The full destination specifier is %v", rns.LogNotice, false, dest)
	time.Sleep(250 * time.Millisecond)
	os.Exit(0)
}

func (a *appT) doAnnounce(ts rns.Transport, id *rns.Identity, aspects string) {
	logger := a.getLogger()
	parts := strings.Split(aspects, ".")
	if len(parts) < 2 {
		logMessage(logger, "Invalid destination aspects specified", rns.LogError, false)
		os.Exit(32)
	}
	appName := parts[0]
	subAspects := parts[1:]

	if id.GetPrivateKey() != nil {
		dest, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, appName, subAspects...)
		if err != nil {
			logMessage(logger, "An error ocurred while attempting to send the announce.", rns.LogError, false)
			logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			os.Exit(32)
		}
		logf(logger, "Created destination %v", rns.LogNotice, false, dest)
		logf(logger, "Announcing destination %v", rns.LogNotice, false, rns.PrettyHex(dest.Hash))
		time.Sleep(1100 * time.Millisecond)
		if err := dest.Announce(nil); err != nil {
			logMessage(logger, "An error ocurred while attempting to send the announce.", rns.LogError, false)
			logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			os.Exit(32)
		}
		time.Sleep(250 * time.Millisecond)
		os.Exit(0)
	}

	dest, err := rns.NewDestination(ts, id, rns.DestinationOut, rns.DestinationSingle, appName, subAspects...)
	if err != nil {
		logMessage(logger, "An error ocurred while attempting to send the announce.", rns.LogError, false)
		logf(logger, "The contained exception was: %v", rns.LogError, false, err)
		os.Exit(32)
	}
	logf(logger, "The %v destination for this Identity is %v", rns.LogNotice, false, aspects, rns.PrettyHex(dest.Hash))
	logf(logger, "The full destination specifier is %v", rns.LogNotice, false, dest)
	logMessage(logger, "Cannot announce this destination, since the private key is not held", rns.LogNotice, false)
	time.Sleep(250 * time.Millisecond)
	os.Exit(33)
}

func expandUser(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}

const chunkSize = 16 * 1024 * 1024

func (a *appT) doFileOps(id *rns.Identity, readPath, writePath, encFile, decFile, sgnFile, valFile string, force, stdout bool) {
	logger := a.getLogger()
	idStr := rns.PrettyHexFromString(id.HexHash)

	if valFile != "" {
		if _, err := os.Stat(valFile); err != nil {
			logf(logger, "Signature file %v not found", rns.LogError, false, readPath)
			os.Exit(10)
		}
		if readPath == "" {
			logMessage(logger, "Signature verification requested, but no input data specified", rns.LogError, false)
			os.Exit(11)
		}
		if _, err := os.Stat(readPath); err != nil {
			logf(logger, "Input file %v not found", rns.LogError, false, readPath)
			os.Exit(11)
		}
	}

	var dataInput *os.File
	if readPath != "" {
		if _, err := os.Stat(readPath); err != nil {
			logf(logger, "Input file %v not found", rns.LogError, false, readPath)
			os.Exit(12)
		}
		f, err := os.Open(readPath)
		if err != nil {
			logMessage(logger, "Could not open input file for reading", rns.LogError, false)
			logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			os.Exit(13)
		}
		defer func() {
			if err := f.Close(); err != nil {
				logf(logger, "Warning: Could not close file %v properly: %v", rns.LogWarning, false, f.Name(), err)
			}
		}()
		dataInput = f
	}

	if encFile != "" && writePath == "" && !stdout && readPath != "" {
		writePath = readPath + ".rfe"
	}
	if decFile != "" && writePath == "" && !stdout && readPath != "" && strings.HasSuffix(strings.ToLower(readPath), ".rfe") {
		writePath = strings.Replace(readPath, ".rfe", "", 1)
	}
	if sgnFile != "" && id.GetPrivateKey() == nil {
		logMessage(logger, "Specified Identity does not hold a private key. Cannot sign.", rns.LogError, false)
		os.Exit(14)
	}
	if sgnFile != "" && writePath == "" && !stdout && readPath != "" {
		writePath = readPath + ".rsg"
	}

	var dataOutput *os.File
	if writePath != "" {
		if !force {
			if _, err := os.Stat(writePath); err == nil {
				logf(logger, "Output file %v already exists. Not overwriting.", rns.LogError, false, writePath)
				os.Exit(15)
			}
		}
		f, err := os.Create(writePath)
		if err != nil {
			logMessage(logger, "Could not open output file for writing", rns.LogError, false)
			logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			os.Exit(15)
		}
		defer func() {
			if err := f.Close(); err != nil {
				logf(logger, "Warning: Could not close file %v properly: %v", rns.LogWarning, false, f.Name(), err)
			}
		}()
		dataOutput = f
	}

	if sgnFile != "" {
		if id.GetPrivateKey() == nil {
			logMessage(logger, "Specified Identity does not hold a private key. Cannot sign.", rns.LogError, false)
			os.Exit(16)
		}
		if dataInput == nil {
			if !stdout {
				logMessage(logger, "Signing requested, but no input data specified", rns.LogError, false)
			}
			os.Exit(17)
		}
		if dataOutput == nil {
			if !stdout {
				logMessage(logger, "Signing requested, but no output specified", rns.LogError, false)
			}
			os.Exit(18)
		}
		if !stdout {
			logf(logger, "Signing %v", rns.LogNotice, false, readPath)
		}
		data, err := os.ReadFile(readPath)
		if err != nil {
			if !stdout {
				logMessage(logger, "An error ocurred while encrypting data.", rns.LogError, false)
				logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			}
			os.Exit(19)
		}
		sig, err := id.Sign(data)
		if err != nil {
			if !stdout {
				logMessage(logger, "An error ocurred while encrypting data.", rns.LogError, false)
				logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			}
			os.Exit(19)
		}
		if _, err := dataOutput.Write(sig); err != nil {
			if !stdout {
				logMessage(logger, "An error ocurred while encrypting data.", rns.LogError, false)
				logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			}
			os.Exit(19)
		}
		if !stdout && readPath != "" {
			logf(logger, "File %v signed with %v to %v", rns.LogNotice, false, readPath, idStr, writePath)
		}
		os.Exit(0)
	}

	if valFile != "" {
		if dataInput == nil {
			if !stdout {
				logMessage(logger, "Signature verification requested, but no input data specified", rns.LogError, false)
			}
			os.Exit(20)
		}
		sigData, err := os.ReadFile(valFile)
		if err != nil {
			logf(logger, "An error ocurred while opening %v.", rns.LogError, false, valFile)
			logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			os.Exit(21)
		}
		inputData, err := os.ReadFile(readPath)
		if err != nil {
			if !stdout {
				logMessage(logger, "An error ocurred while validating signature.", rns.LogError, false)
				logf(logger, "The contained exception was: %v", rns.LogError, false, err)
			}
			os.Exit(23)
		}
		if !id.Verify(sigData, inputData) {
			if !stdout {
				logf(logger, "Signature %v for file %v is invalid", rns.LogError, false, valFile, readPath)
			}
			os.Exit(22)
		}
		if !stdout {
			logf(logger, "Signature %v for file %v made by Identity %v is valid", rns.LogNotice, false, valFile, readPath, idStr)
		}
		os.Exit(0)
	}

	if encFile != "" {
		if dataInput == nil {
			if !stdout {
				logMessage(logger, "Encryption requested, but no input data specified", rns.LogError, false)
			}
			os.Exit(24)
		}
		if dataOutput == nil {
			if !stdout {
				logMessage(logger, "Encryption requested, but no output specified", rns.LogError, false)
			}
			os.Exit(25)
		}
		if !stdout {
			logf(logger, "Encrypting %v", rns.LogNotice, false, readPath)
		}
		buf := make([]byte, chunkSize)
		for {
			n, err := dataInput.Read(buf)
			if n > 0 {
				ct, encErr := id.Encrypt(buf[:n], nil)
				if encErr != nil {
					if !stdout {
						logMessage(logger, "An error ocurred while encrypting data.", rns.LogError, false)
						logf(logger, "The contained exception was: %v", rns.LogError, false, encErr)
					}
					os.Exit(26)
				}
				if _, wErr := dataOutput.Write(ct); wErr != nil {
					if !stdout {
						logMessage(logger, "An error ocurred while encrypting data.", rns.LogError, false)
						logf(logger, "The contained exception was: %v", rns.LogError, false, wErr)
					}
					os.Exit(26)
				}
			}
			if err != nil {
				break
			}
		}
		if !stdout && readPath != "" {
			logf(logger, "File %v encrypted for %v to %v", rns.LogNotice, false, readPath, idStr, writePath)
		}
		os.Exit(0)
	}

	if decFile != "" {
		if id.GetPrivateKey() == nil {
			logMessage(logger, "Specified Identity does not hold a private key. Cannot decrypt.", rns.LogError, false)
			os.Exit(27)
		}
		if dataInput == nil {
			if !stdout {
				logMessage(logger, "Decryption requested, but no input data specified", rns.LogError, false)
			}
			os.Exit(28)
		}
		if dataOutput == nil {
			if !stdout {
				logMessage(logger, "Decryption requested, but no output specified", rns.LogError, false)
			}
			os.Exit(29)
		}
		if !stdout {
			logf(logger, "Decrypting %v...", rns.LogNotice, false, readPath)
		}
		buf := make([]byte, chunkSize)
		for {
			n, err := dataInput.Read(buf)
			if n > 0 {
				plaintext, decErr := id.Decrypt(buf[:n], nil, false)
				if decErr != nil || plaintext == nil {
					if !stdout {
						logMessage(logger, "Data could not be decrypted with the specified Identity", rns.LogNotice, false)
					}
					os.Exit(30)
				}
				if _, wErr := dataOutput.Write(plaintext); wErr != nil {
					if !stdout {
						logMessage(logger, "An error ocurred while decrypting data.", rns.LogError, false)
						logf(logger, "The contained exception was: %v", rns.LogError, false, wErr)
					}
					os.Exit(31)
				}
			}
			if err != nil {
				break
			}
		}
		if !stdout && readPath != "" {
			logf(logger, "File %v decrypted with %v to %v", rns.LogNotice, false, readPath, idStr, writePath)
		}
		os.Exit(0)
	}
}
