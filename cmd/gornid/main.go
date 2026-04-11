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

type runtimeT struct {
	app    *appT
	logger *rns.Logger
}

func newRuntime(app *appT) *runtimeT {
	if app == nil {
		app = newApp()
	}
	logger := rns.NewLogger()
	app.logger = logger
	return &runtimeT{app: app, logger: logger}
}

func (a *appT) run() int {
	if a.logger == nil {
		a.logger = rns.NewLogger()
	}
	logger := a.logger
	var ops int
	for _, op := range []bool{a.encryptFile != "", a.decryptFile != "", a.validateFile != "", a.signFile != ""} {
		if op {
			ops++
		}
	}

	if ops > 1 {
		logger.Error("This utility currently only supports one of the encrypt, decrypt, sign or verify operations per invocation")
		return 1
	}

	if a.version {
		utils.PrintVersion(os.Stdout, "gornid", rns.VERSION)
		return 0
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
		return a.doImport(a.importStr, a.useBase64, a.useBase32, a.printPrivate, a.writeFile, a.force)
	}

	if a.generatePath == "" && a.identityPath == "" {
		_, _ = fmt.Fprint(os.Stderr, "\nNo identity provided, cannot continue\n")
		a.usage(os.Stderr)
		return 2
	}

	ts := rns.NewTransportSystem(a.logger)
	ret, err := rns.NewReticulumWithLogger(ts, a.configDir, logger)
	if err != nil {
		logger.Error("Could not initialize Reticulum: %v", err)
		return 1
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum properly: %v", err)
		}
	}()

	logger.SetCompactLogFmt(true)
	if a.useStdout {
		logger.SetLogLevel(-1)
	}

	if a.generatePath != "" {
		return a.doGenerate(a.generatePath, a.force)
	}

	id, exitCode := a.loadIdentity(ret.Transport(), a.identityPath, a.requestID, a.timeout)
	if id == nil {
		if exitCode != 0 {
			return exitCode
		}
		logger.Error("Could not load or recall identity")
		return 1
	}

	if a.printIdentity {
		return a.doPrintIdentity(id, a.useBase64, a.useBase32, a.printPrivate)
	}

	if a.export {
		return a.doExport(id, a.useBase64, a.useBase32)
	}

	if a.hashAspects != "" {
		return a.doHash(ts, id, a.hashAspects)
	}

	if a.announce != "" {
		return a.doAnnounce(ts, id, a.announce)
	}

	if a.encryptFile != "" || a.decryptFile != "" || a.signFile != "" || a.validateFile != "" {
		return a.doFileOps(id, a.readFile, a.writeFile, a.encryptFile, a.decryptFile, a.signFile, a.validateFile, a.force, a.useStdout)
	}

	return 0
}

func main() {
	log.SetFlags(0)
	app, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if err == utils.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(newRuntime(app).run())
}

func (rt *runtimeT) run() int {
	if rt == nil || rt.app == nil {
		return 1
	}
	rt.app.logger = rt.logger
	return rt.app.run()
}

func (rt *runtimeT) loadIdentity(ts rns.Transport, path string, request bool, timeout float64) (*rns.Identity, int) {
	if rt == nil || rt.app == nil {
		return nil, 1
	}
	rt.app.logger = rt.logger
	return rt.app.loadIdentity(ts, path, request, timeout)
}

func (a *appT) doImport(data string, b64, b32, prv bool, writePath string, force bool) int {
	logger := a.logger
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
		return 41
	}

	id, err := rns.FromBytes(idBytes, a.logger)
	if err != nil {
		fmt.Printf("Could not create Reticulum identity from specified data: %v\n", err)
		return 42
	}

	logger.Notice("Identity imported")
	a.doPrintIdentity(id, b64, b32, prv)

	if writePath != "" {
		wp := expandUser(writePath)
		if !force {
			if _, err := os.Stat(wp); err == nil {
				fmt.Printf("File %v already exists, not overwriting\n", wp)
				return 43
			}
		}
		if err := id.ToFile(wp); err != nil {
			fmt.Printf("Error while writing imported identity to file: %v\n", err)
			return 44
		}
		logger.Notice("Wrote imported identity to %v", writePath)
	}
	return 0
}

func (a *appT) doGenerate(path string, force bool) int {
	logger := a.logger
	if _, err := os.Stat(path); err == nil && !force {
		logger.Error("Identity file %v already exists. Not overwriting.", path)
		return 3
	}
	id, err := rns.NewIdentity(true, a.logger)
	if err != nil {
		logger.Error("An error ocurred while saving the generated Identity: %v", err)
		return 4
	}
	if err := id.ToFile(path); err != nil {
		logger.Error("An error ocurred while saving the generated Identity: %v", err)
		return 4
	}
	logger.Notice("New identity %v written to %v", rns.PrettyHexFromString(id.HexHash), path)
	return 0
}

func (a *appT) loadIdentity(ts rns.Transport, path string, request bool, timeout float64) (*rns.Identity, int) {
	logger := a.logger
	if path == "" {
		return nil, 0
	}

	hashStrLen := rns.TruncatedHashLength / 8 * 2
	_, fileErr := os.Stat(path)
	isFile := fileErr == nil

	if len(path) == hashStrLen && !isFile {
		hash, err := hex.DecodeString(path)
		if err != nil {
			logger.Error("Invalid hexadecimal hash provided")
			return nil, 7
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
				logger.Error("Could not recall Identity for %v.", rns.PrettyHex(hash))
				logger.Error("You can query the network for unknown Identities with the -R option.")
				return nil, 5
			}
			if err := ts.RequestPath(hash); err != nil {
				logger.Error("Identity request failed for %v: %v", rns.PrettyHex(hash), err)
				return nil, 6
			}
			deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
			for time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				id = ts.Recall(hash)
				if id != nil {
					logger.Notice("Received Identity %v for destination %v from the network", rns.PrettyHexFromString(id.HexHash), rns.PrettyHex(hash))
					return id, 0
				}
			}
			logger.Error("Identity request timed out")
			return nil, 6
		}

		identStr := rns.PrettyHexFromString(id.HexHash)
		hashStr := rns.PrettyHex(hash)
		if identStr == hashStr {
			logger.Notice("Recalled Identity %v", identStr)
		} else {
			logger.Notice("Recalled Identity %v for destination %v", identStr, hashStr)
		}
		return id, 0
	}

	if !isFile {
		logger.Notice("Specified Identity file not found")
		return nil, 8
	}
	id, err := rns.FromFile(path, a.logger)
	if err != nil {
		logger.Notice("Could not decode Identity from specified file")
		return nil, 9
	}
	logger.Notice("Loaded Identity %v from %v", rns.PrettyHexFromString(id.HexHash), path)
	return id, 0
}

func (a *appT) doPrintIdentity(id *rns.Identity, b64, b32, prv bool) int {
	logger := a.logger
	pub := id.GetPublicKey()
	var pubStr string
	if b64 {
		pubStr = base64.URLEncoding.EncodeToString(pub)
	} else if b32 {
		pubStr = base32.StdEncoding.EncodeToString(pub)
	} else {
		pubStr = hex.EncodeToString(pub)
	}
	logger.Notice("Public Key  : %v", pubStr)

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
			logger.Notice("Private Key : %v", privStr)
		} else {
			logger.Notice("Private Key : Hidden")
		}
	}
	return 0
}

func (a *appT) doExport(id *rns.Identity, b64, b32 bool) int {
	logger := a.logger
	priv := id.GetPrivateKey()
	if priv == nil {
		logger.Notice("Identity doesn't hold a private key, cannot export")
		return 50
	}
	var privStr string
	if b64 {
		privStr = base64.URLEncoding.EncodeToString(priv)
	} else if b32 {
		privStr = base32.StdEncoding.EncodeToString(priv)
	} else {
		privStr = hex.EncodeToString(priv)
	}
	logger.Notice("Exported Identity : %v", privStr)
	return 0
}

func (a *appT) doHash(ts rns.Transport, id *rns.Identity, aspects string) int {
	logger := a.logger
	parts := strings.Split(aspects, ".")
	if len(parts) == 0 {
		logger.Error("Invalid destination aspects specified")
		return 32
	}
	appName := parts[0]
	var subAspects []string
	if len(parts) > 1 {
		subAspects = parts[1:]
	}

	if id.GetPublicKey() == nil {
		logger.Error("An error ocurred while attempting to send the announce.")
		logger.Error("The contained exception was: No public key known")
		return 0
	}
	dest, err := rns.NewDestination(ts, id, rns.DestinationOut, rns.DestinationSingle, appName, subAspects...)
	if err != nil {
		logger.Error("An error ocurred while attempting to send the announce.")
		logger.Error("The contained exception was: %v", err)
		return 0
	}
	logger.Notice("The %v destination for this Identity is %v", aspects, rns.PrettyHex(dest.Hash))
	logger.Notice("The full destination specifier is %v", dest)
	time.Sleep(250 * time.Millisecond)
	return 0
}

func (a *appT) doAnnounce(ts rns.Transport, id *rns.Identity, aspects string) int {
	logger := a.logger
	parts := strings.Split(aspects, ".")
	if len(parts) < 2 {
		logger.Error("Invalid destination aspects specified")
		return 32
	}
	appName := parts[0]
	subAspects := parts[1:]

	if id.GetPrivateKey() != nil {
		dest, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, appName, subAspects...)
		if err != nil {
			logger.Error("An error ocurred while attempting to send the announce.")
			logger.Error("The contained exception was: %v", err)
			return 32
		}
		logger.Notice("Created destination %v", dest)
		logger.Notice("Announcing destination %v", rns.PrettyHex(dest.Hash))
		time.Sleep(1100 * time.Millisecond)
		if err := dest.Announce(nil); err != nil {
			logger.Error("An error ocurred while attempting to send the announce.")
			logger.Error("The contained exception was: %v", err)
			return 32
		}
		time.Sleep(250 * time.Millisecond)
		return 0
	}

	dest, err := rns.NewDestination(ts, id, rns.DestinationOut, rns.DestinationSingle, appName, subAspects...)
	if err != nil {
		logger.Error("An error ocurred while attempting to send the announce.")
		logger.Error("The contained exception was: %v", err)
		return 32
	}
	logger.Notice("The %v destination for this Identity is %v", aspects, rns.PrettyHex(dest.Hash))
	logger.Notice("The full destination specifier is %v", dest)
	logger.Notice("Cannot announce this destination, since the private key is not held")
	time.Sleep(250 * time.Millisecond)
	return 33
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

func (a *appT) doFileOps(id *rns.Identity, readPath, writePath, encFile, decFile, sgnFile, valFile string, force, stdout bool) int {
	logger := a.logger
	idStr := rns.PrettyHexFromString(id.HexHash)

	if valFile != "" {
		if _, err := os.Stat(valFile); err != nil {
			logger.Error("Signature file %v not found", valFile)
			return 10
		}
		if readPath == "" {
			logger.Error("Signature verification requested, but no input data specified")
			return 11
		}
		if _, err := os.Stat(readPath); err != nil {
			logger.Error("Input file %v not found", readPath)
			return 11
		}
	}

	var dataInput *os.File
	if readPath != "" {
		if _, err := os.Stat(readPath); err != nil {
			logger.Error("Input file %v not found", readPath)
			return 12
		}
		f, err := os.Open(readPath)
		if err != nil {
			logger.Error("Could not open input file for reading")
			logger.Error("The contained exception was: %v", err)
			return 13
		}
		defer func() {
			if err := f.Close(); err != nil {
				logger.Warning("Could not close file %v properly: %v", f.Name(), err)
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
		logger.Error("Specified Identity does not hold a private key. Cannot sign.")
		return 14
	}
	if sgnFile != "" && writePath == "" && !stdout && readPath != "" {
		writePath = readPath + ".rsg"
	}

	var dataOutput *os.File
	if writePath != "" {
		if !force {
			if _, err := os.Stat(writePath); err == nil {
				logger.Error("Output file %v already exists. Not overwriting.", writePath)
				return 15
			}
		}
		f, err := os.Create(writePath)
		if err != nil {
			logger.Error("Could not open output file for writing")
			logger.Error("The contained exception was: %v", err)
			return 15
		}
		defer func() {
			if err := f.Close(); err != nil {
				logger.Warning("Could not close file %v properly: %v", f.Name(), err)
			}
		}()
		dataOutput = f
	}

	if sgnFile != "" {
		if id.GetPrivateKey() == nil {
			logger.Error("Specified Identity does not hold a private key. Cannot sign.")
			return 16
		}
		if dataInput == nil {
			if !stdout {
				logger.Error("Signing requested, but no input data specified")
			}
			return 17
		}
		if dataOutput == nil {
			if !stdout {
				logger.Error("Signing requested, but no output specified")
			}
			return 18
		}
		if !stdout {
			logger.Notice("Signing %v", readPath)
		}
		data, err := os.ReadFile(readPath)
		if err != nil {
			if !stdout {
				logger.Error("An error ocurred while encrypting data.")
				logger.Error("The contained exception was: %v", err)
			}
			return 19
		}
		sig, err := id.Sign(data)
		if err != nil {
			if !stdout {
				logger.Error("An error ocurred while encrypting data.")
				logger.Error("The contained exception was: %v", err)
			}
			return 19
		}
		if _, err := dataOutput.Write(sig); err != nil {
			if !stdout {
				logger.Error("An error ocurred while encrypting data.")
				logger.Error("The contained exception was: %v", err)
			}
			return 19
		}
		if !stdout && readPath != "" {
			logger.Notice("File %v signed with %v to %v", readPath, idStr, writePath)
		}
		return 0
	}

	if valFile != "" {
		if dataInput == nil {
			if !stdout {
				logger.Error("Signature verification requested, but no input data specified")
			}
			return 20
		}
		sigData, err := os.ReadFile(valFile)
		if err != nil {
			logger.Error("An error ocurred while opening %v.", valFile)
			logger.Error("The contained exception was: %v", err)
			return 21
		}
		inputData, err := os.ReadFile(readPath)
		if err != nil {
			if !stdout {
				logger.Error("An error ocurred while validating signature.")
				logger.Error("The contained exception was: %v", err)
			}
			return 23
		}
		if !id.Verify(sigData, inputData) {
			if !stdout {
				logger.Error("Signature %v for file %v is invalid", valFile, readPath)
			}
			return 22
		}
		if !stdout {
			logger.Notice("Signature %v for file %v made by Identity %v is valid", valFile, readPath, idStr)
		}
		return 0
	}

	if encFile != "" {
		if dataInput == nil {
			if !stdout {
				logger.Error("Encryption requested, but no input data specified")
			}
			return 24
		}
		if dataOutput == nil {
			if !stdout {
				logger.Error("Encryption requested, but no output specified")
			}
			return 25
		}
		if !stdout {
			logger.Notice("Encrypting %v", readPath)
		}
		buf := make([]byte, chunkSize)
		for {
			n, err := dataInput.Read(buf)
			if n > 0 {
				ct, encErr := id.Encrypt(buf[:n], nil)
				if encErr != nil {
					if !stdout {
						logger.Error("An error ocurred while encrypting data.")
						logger.Error("The contained exception was: %v", encErr)
					}
					return 26
				}
				if _, wErr := dataOutput.Write(ct); wErr != nil {
					if !stdout {
						logger.Error("An error ocurred while encrypting data.")
						logger.Error("The contained exception was: %v", wErr)
					}
					return 26
				}
			}
			if err != nil {
				break
			}
		}
		if !stdout && readPath != "" {
			logger.Notice("File %v encrypted for %v to %v", readPath, idStr, writePath)
		}
		return 0
	}

	if decFile != "" {
		if id.GetPrivateKey() == nil {
			logger.Error("Specified Identity does not hold a private key. Cannot decrypt.")
			return 27
		}
		if dataInput == nil {
			if !stdout {
				logger.Error("Decryption requested, but no input data specified")
			}
			return 28
		}
		if dataOutput == nil {
			if !stdout {
				logger.Error("Decryption requested, but no output specified")
			}
			return 29
		}
		if !stdout {
			logger.Notice("Decrypting %v...", readPath)
		}
		buf := make([]byte, chunkSize)
		for {
			n, err := dataInput.Read(buf)
			if n > 0 {
				plaintext, decErr := id.Decrypt(buf[:n], nil, false)
				if decErr != nil || plaintext == nil {
					if !stdout {
						logger.Notice("Data could not be decrypted with the specified Identity")
					}
					return 30
				}
				if _, wErr := dataOutput.Write(plaintext); wErr != nil {
					if !stdout {
						logger.Error("An error ocurred while decrypting data.")
						logger.Error("The contained exception was: %v", wErr)
					}
					return 31
				}
			}
			if err != nil {
				break
			}
		}
		if !stdout && readPath != "" {
			logger.Notice("File %v decrypted with %v to %v", readPath, idStr, writePath)
		}
		return 0
	}
	return 0
}
