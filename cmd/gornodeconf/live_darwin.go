//go:build darwin

package main

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const (
	rnodeReadLoopFrameLimit = 1024

	romPlatformESP32 = 0x80
	romPlatformNRF52 = 0x70
	romPlatformAVR   = 0x90

	rnodeKISSCommandROMWrite = 0x52
)

type modeSwitchWriter interface {
	Write([]byte) (int, error)
}

type modeSwitchSleeper interface {
	Sleep(time.Duration)
}

type modeSwitchState struct {
	platform byte
	writer   modeSwitchWriter
	sleeper  modeSwitchSleeper
}

func rnodeSetNormalMode(writer modeSwitchWriter) error {
	return writeModeCommand(writer, []byte{kissFend, 0x54, 0x00, kissFend}, "configuring device mode")
}

func (s *modeSwitchState) setTNCMode() error {
	if err := writeModeCommand(s.writer, []byte{kissFend, 0x53, 0x00, kissFend}, "configuring device mode"); err != nil {
		return err
	}
	if s.platform == romPlatformESP32 {
		if err := s.hardReset(); err != nil {
			return err
		}
	}
	return nil
}

func (s *modeSwitchState) wipeEEPROM() error {
	if err := writeModeCommand(s.writer, []byte{kissFend, 0x59, 0xf8, kissFend}, "wiping EEPROM"); err != nil {
		return err
	}
	s.sleeper.Sleep(13 * time.Second)
	if s.platform == romPlatformNRF52 {
		s.sleeper.Sleep(10 * time.Second)
	}
	return nil
}

func (s *modeSwitchState) hardReset() error {
	if err := writeModeCommand(s.writer, []byte{kissFend, 0x55, 0xf8, kissFend}, "restarting device"); err != nil {
		return err
	}
	s.sleeper.Sleep(2 * time.Second)
	return nil
}

func writeModeCommand(writer modeSwitchWriter, command []byte, action string) error {
	written, err := writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return fmt.Errorf("An IO error occurred while %v", action)
	}
	return nil
}

type rnodeReadLoopFrame struct {
	command byte
	payload []byte
}

type rnodeReadLoopState struct {
	inFrame            bool
	escape             bool
	command            byte
	dataBuffer         []byte
	commandBuffer      []byte
	rFrequency         int
	rBandwidth         int
	majorVersion       byte
	minorVersion       byte
	platform           byte
	deviceHash         []byte
	firmwareHash       []byte
	firmwareHashTarget []byte
	hashTimeoutErr     error
	eepromTimeoutErr   error
}

func newRnodeReadLoopState() *rnodeReadLoopState {
	return &rnodeReadLoopState{command: rnodeKISSCommandUnknown}
}

func (s *rnodeReadLoopState) hashesTimeoutError() error {
	if s.hashTimeoutErr == nil {
		s.hashTimeoutErr = errors.New("timed out while reading device hashes")
	}
	return s.hashTimeoutErr
}

func (s *rnodeReadLoopState) eepromTimeoutError() error {
	if s.eepromTimeoutErr == nil {
		s.eepromTimeoutErr = errors.New("timed out while reading device EEPROM")
	}
	return s.eepromTimeoutErr
}

func (s *rnodeReadLoopState) feedByte(b byte) (rnodeReadLoopFrame, bool) {
	if s.inFrame && b == kissFend && s.isPayloadCommand() {
		frame := rnodeReadLoopFrame{command: s.command, payload: append([]byte(nil), s.dataBuffer...)}
		s.resetFrame()
		return frame, true
	}

	if b == kissFend {
		s.inFrame = true
		s.escape = false
		s.command = rnodeKISSCommandUnknown
		s.dataBuffer = s.dataBuffer[:0]
		s.commandBuffer = s.commandBuffer[:0]
		return rnodeReadLoopFrame{}, false
	}

	if !s.inFrame || len(s.dataBuffer) >= rnodeReadLoopFrameLimit {
		return rnodeReadLoopFrame{}, false
	}

	if len(s.dataBuffer) == 0 && s.command == rnodeKISSCommandUnknown {
		s.command = b
		return rnodeReadLoopFrame{}, false
	}

	if s.command != rnodeKISSCommandROMRead && s.command != rnodeKISSCommandCFGRead && s.command != rnodeKISSCommandData && s.command != rnodeKISSCommandFrequency && s.command != rnodeKISSCommandBandwidth && s.command != rnodeKISSCommandPlatform && s.command != rnodeKISSCommandFWVersion && s.command != rnodeKISSCommandDevHash && s.command != rnodeKISSCommandHashes {
		return rnodeReadLoopFrame{}, false
	}

	if b == kissFesc {
		s.escape = true
		return rnodeReadLoopFrame{}, false
	}

	if s.escape {
		s.escape = false
		b = decodeKISSEscapedByte(b)
	}

	if s.command == rnodeKISSCommandFrequency || s.command == rnodeKISSCommandBandwidth || s.command == rnodeKISSCommandPlatform || s.command == rnodeKISSCommandFWVersion || s.command == rnodeKISSCommandDevHash || s.command == rnodeKISSCommandHashes {
		s.commandBuffer = append(s.commandBuffer, b)
		s.applyCommandBuffer()
		return rnodeReadLoopFrame{}, false
	}

	s.dataBuffer = append(s.dataBuffer, b)
	return rnodeReadLoopFrame{}, false
}

func (s *rnodeReadLoopState) applyCommandBuffer() {
	switch s.command {
	case rnodeKISSCommandFrequency:
		if len(s.commandBuffer) == 4 {
			s.rFrequency = int(s.commandBuffer[0])<<24 | int(s.commandBuffer[1])<<16 | int(s.commandBuffer[2])<<8 | int(s.commandBuffer[3])
		}
	case rnodeKISSCommandBandwidth:
		if len(s.commandBuffer) == 4 {
			s.rBandwidth = int(s.commandBuffer[0])<<24 | int(s.commandBuffer[1])<<16 | int(s.commandBuffer[2])<<8 | int(s.commandBuffer[3])
		}
	case rnodeKISSCommandFWVersion:
		if len(s.commandBuffer) == 2 {
			s.majorVersion = s.commandBuffer[0]
			s.minorVersion = s.commandBuffer[1]
		}
	case rnodeKISSCommandPlatform:
		if len(s.commandBuffer) == 1 {
			s.platform = s.commandBuffer[0]
		}
	case rnodeKISSCommandDevHash:
		if len(s.commandBuffer) == 32 {
			s.deviceHash = append([]byte(nil), s.commandBuffer...)
		}
	case rnodeKISSCommandHashes:
		if len(s.commandBuffer) == 33 {
			if s.commandBuffer[0] == 0x01 {
				s.firmwareHashTarget = append([]byte(nil), s.commandBuffer[1:]...)
			}
			if s.commandBuffer[0] == 0x02 {
				s.firmwareHash = append([]byte(nil), s.commandBuffer[1:]...)
			}
		}
	}
}

func (s *rnodeReadLoopState) isPayloadCommand() bool {
	switch s.command {
	case rnodeKISSCommandROMRead, rnodeKISSCommandCFGRead, rnodeKISSCommandData:
		return true
	default:
		return false
	}
}

func (s *rnodeReadLoopState) resetFrame() {
	s.inFrame = false
	s.escape = false
	s.command = rnodeKISSCommandUnknown
	s.dataBuffer = s.dataBuffer[:0]
	s.commandBuffer = s.commandBuffer[:0]
}

func (s *rnodeReadLoopState) resetForIdleTimeout() {
	s.inFrame = false
	s.escape = false
	s.command = rnodeKISSCommandUnknown
	s.dataBuffer = s.dataBuffer[:0]
}

func (s *rnodeReadLoopState) idleTimeoutExpired(nowMs, lastReadMs, timeoutMs int) bool {
	if len(s.dataBuffer) > 0 && nowMs-lastReadMs > timeoutMs {
		s.resetForIdleTimeout()
		return true
	}
	return false
}

func (s *rnodeReadLoopState) shutdownCleanup() {
	s.resetFrame()
}

func decodeKISSEscapedByte(b byte) byte {
	if b == kissTfend {
		return kissFend
	}
	if b == kissTfesc {
		return kissFesc
	}
	return b
}

type eepromDownloaderWriter interface {
	Write([]byte) (int, error)
}

type eepromDownloaderSleeper interface {
	Sleep(time.Duration)
}

type eepromDownloaderState struct {
	name           string
	eeprom         []byte
	cfgSector      []byte
	provisioned    bool
	configured     bool
	product        byte
	model          byte
	hwRev          byte
	serialno       []byte
	made           []byte
	checksum       []byte
	signature      []byte
	minFreq        int
	maxFreq        int
	maxOutput      int
	confSF         byte
	confCR         byte
	confTXPower    byte
	confFrequency  int
	confBandwidth  int
	vendor         string
	locallySigned  bool
	signatureValid bool
	version        string
	writer         eepromDownloaderWriter
	sleeper        eepromDownloaderSleeper
	parse          func() error
	metadata       eepromMetadataState
}

type eepromMetadataState struct {
	modelCapabilitiesByCode map[byte]modelCapability
	productNames            map[byte]string
}

func newEEPROMMetadataState() eepromMetadataState {
	return eepromMetadataState{
		modelCapabilitiesByCode: map[byte]modelCapability{0xa4: {minFreq: 410000000, maxFreq: 525000000, maxOutput: 14, summary: "410 - 525 MHz", modem: "SX1278"}},
		productNames:            map[byte]string{0x03: "RNode"},
	}
}

func (s *eepromDownloaderState) metadataState() eepromMetadataState {
	if len(s.metadata.modelCapabilitiesByCode) == 0 && len(s.metadata.productNames) == 0 {
		return newEEPROMMetadataState()
	}
	return s.metadata
}

func (s *eepromDownloaderState) downloadEEPROM() error {
	s.eeprom = nil
	command := []byte{kissFend, 0x51, 0x00, kissFend}
	written, err := s.writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return errors.New("An IO error occurred while downloading EEPROM")
	}

	s.sleeper.Sleep(600 * time.Millisecond)
	if s.eeprom == nil {
		return errors.New("Could not download EEPROM from device. Is a valid firmware installed?")
	}
	if s.parse != nil {
		return s.parse()
	}
	return nil
}

func (s *eepromDownloaderState) downloadCfgSector() error {
	s.cfgSector = nil
	command := []byte{kissFend, 0x6d, 0x00, kissFend}
	written, err := s.writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return errors.New("An IO error occurred while downloading config sector")
	}

	s.sleeper.Sleep(600 * time.Millisecond)
	return nil
}

func (s *eepromDownloaderState) parseEEPROM() error {
	if len(s.eeprom) == 0 {
		return errors.New("Invalid EEPROM data, could not parse device EEPROM.")
	}
	if s.eeprom[0x9b] != 0x73 {
		s.provisioned = false
		return errors.New("EEPROM is invalid, no further information available")
	}

	s.provisioned = true
	s.product = s.eeprom[0x00]
	s.model = s.eeprom[0x01]
	s.hwRev = s.eeprom[0x02]
	s.serialno = append([]byte(nil), s.eeprom[0x03:0x07]...)
	s.made = append([]byte(nil), s.eeprom[0x07:0x0b]...)
	s.checksum = append([]byte(nil), s.eeprom[0x0b:0x1b]...)
	s.signature = append([]byte(nil), s.eeprom[0x1b:0x9b]...)

	metadata := s.metadataState()
	if info, ok := metadata.modelCapabilitiesByCode[s.model]; ok {
		s.minFreq = info.minFreq
		s.maxFreq = info.maxFreq
		s.maxOutput = info.maxOutput
	} else {
		s.minFreq = 0
		s.maxFreq = 0
		s.maxOutput = 0
	}

	checksummedInfo := []byte{s.product, s.model, s.hwRev}
	checksummedInfo = append(checksummedInfo, s.serialno...)
	checksummedInfo = append(checksummedInfo, s.made...)
	digest := md5.Sum(checksummedInfo)
	if !bytesEqual(s.checksum, digest[:]) {
		s.provisioned = false
		return errors.New("EEPROM checksum mismatch")
	}

	if s.eeprom[0xa7] == 0x73 {
		s.configured = true
		s.confSF = s.eeprom[0x9c]
		s.confCR = s.eeprom[0x9d]
		s.confTXPower = s.eeprom[0x9e]
		s.confBandwidth = int(s.eeprom[0x9f])<<24 | int(s.eeprom[0xa0])<<16 | int(s.eeprom[0xa1])<<8 | int(s.eeprom[0xa2])
		s.confFrequency = int(s.eeprom[0xa3])<<24 | int(s.eeprom[0xa4])<<16 | int(s.eeprom[0xa5])<<8 | int(s.eeprom[0xa6])
	} else {
		s.configured = false
	}

	return nil
}

func (s *eepromDownloaderState) deviceInfoLines(timestring string) []string {
	sigstring := "Unverified"
	if s.signatureValid {
		if s.locallySigned {
			sigstring = "Validated - Local signature"
		} else {
			sigstring = "Genuine board, vendor is " + s.vendor
		}
	}

	metadata := s.metadataState()
	productName := metadata.productNames[s.product]
	modelInfo := metadata.modelCapabilitiesByCode[s.model]

	lines := []string{
		"",
		"Device info:",
		fmt.Sprintf("\tProduct            : %v %v (%02x:%02x)", productName, modelInfo.summary, s.product, s.model),
		"\tDevice signature   : " + sigstring,
		"\tFirmware version   : " + s.version,
		fmt.Sprintf("\tHardware revision  : %v", int(s.hwRev)),
		"\tSerial number      : " + hexBytes(s.serialno),
		"\tModem chip         : " + modelInfo.modem,
		fmt.Sprintf("\tFrequency range    : %v MHz - %v MHz", float64(s.minFreq)/1e6, float64(s.maxFreq)/1e6),
		fmt.Sprintf("\tMax TX power       : %v dBm", s.maxOutput),
		"\tManufactured       : " + timestring,
	}

	if s.configured {
		txpMw := math.Round(math.Pow(10, float64(s.confTXPower)/10.0)*1000) / 1000
		lines = append(lines,
			"",
			"\tDevice mode        : TNC",
			fmt.Sprintf("\t  Frequency        : %v MHz", float64(s.confFrequency)/1000000.0),
			fmt.Sprintf("\t  Bandwidth        : %v KHz", float64(s.confBandwidth)/1000.0),
			fmt.Sprintf("\t  TX power         : %v dBm (%v mW)", s.confTXPower, txpMw),
			fmt.Sprintf("\t  Spreading factor : %v", s.confSF),
			fmt.Sprintf("\t  Coding rate      : %v", s.confCR),
			fmt.Sprintf("\t  On-air bitrate   : %v kbps", s.onAirBitrateKbps()),
		)
	} else {
		lines = append(lines, "\tDevice mode        : Normal (host-controlled)")
	}

	return lines
}

func (s *eepromDownloaderState) onAirBitrateKbps() float64 {
	if s.confBandwidth == 0 || s.confSF == 0 || s.confCR == 0 {
		return 0
	}
	bitrate := float64(s.confSF) * ((4.0 / float64(s.confCR)) / (math.Pow(2, float64(s.confSF)) / (float64(s.confBandwidth) / 1000.0))) * 1000
	return math.Round((bitrate/1000.0)*100) / 100
}

type modelCapability struct {
	minFreq   int
	maxFreq   int
	maxOutput int
	summary   string
	modem     string
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func hexBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	result := make([]byte, 0, len(data)*2)
	for _, value := range data {
		result = append(result, "0123456789abcdef"[value>>4], "0123456789abcdef"[value&0x0f])
	}
	return string(result)
}

type firmwareHashSetterWriter interface {
	Write([]byte) (int, error)
}

type firmwareHashSetterState struct {
	name      string
	hashBytes []byte
	writer    firmwareHashSetterWriter
}

func (s *firmwareHashSetterState) setFirmwareHash() error {
	data := kissEscape(s.hashBytes)
	command := append([]byte{kissFend, 0x58}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending firmware hash to device")
}

type signatureSetterWriter interface {
	Write([]byte) (int, error)
}

type signatureSetterState struct {
	name      string
	signature []byte
	writer    signatureSetterWriter
}

func (s *signatureSetterState) storeSignature() error {
	data := kissEscape(s.signature)
	command := append([]byte{kissFend, 0x57}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending signature to device")
}

func storeSignature(writer eepromDownloaderWriter, sigBytes []byte) error {
	payload := append([]byte{rnodeKISSCommandDeviceSignature}, kissEscape(sigBytes)...)
	command := append([]byte{kissFend}, payload...)
	command = append(command, kissFend)
	written, err := writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return fmt.Errorf("An IO error occurred while storing signature")
	}
	return nil
}

func captureRnodeHashes(port serialPort, timeout time.Duration) (rnodeHashSnapshot, error) {
	state := newRnodeReadLoopState()
	byteCh := make(chan byte, 128)
	errCh := make(chan error, 1)
	readDone := make(chan struct{})

	go func() {
		defer close(readDone)
		buf := make([]byte, 1)
		for {
			n, err := port.Read(buf)
			if n > 0 {
				byteCh <- buf[0]
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					errCh <- err
				}
				return
			}
		}
	}()

	if _, err := port.Write(rnodeDetectCommand()); err != nil {
		_ = port.Close()
		return rnodeHashSnapshot{}, err
	}

	deadline := time.After(timeout)
	for {
		if len(state.deviceHash) == 32 && len(state.firmwareHashTarget) == 32 && len(state.firmwareHash) == 32 {
			return rnodeHashSnapshot{deviceHash: append([]byte(nil), state.deviceHash...), firmwareHashTarget: append([]byte(nil), state.firmwareHashTarget...), firmwareHash: append([]byte(nil), state.firmwareHash...)}, nil
		}

		select {
		case b := <-byteCh:
			state.feedByte(b)
		case err := <-errCh:
			_ = port.Close()
			return rnodeHashSnapshot{}, err
		case <-deadline:
			_ = port.Close()
			return rnodeHashSnapshot{}, state.hashesTimeoutError()
		case <-readDone:
			for len(byteCh) > 0 {
				state.feedByte(<-byteCh)
			}
			if len(state.deviceHash) == 32 && len(state.firmwareHashTarget) == 32 && len(state.firmwareHash) == 32 {
				return rnodeHashSnapshot{deviceHash: append([]byte(nil), state.deviceHash...), firmwareHashTarget: append([]byte(nil), state.firmwareHashTarget...), firmwareHash: append([]byte(nil), state.firmwareHash...)}, nil
			}
			_ = port.Close()
			return rnodeHashSnapshot{}, errors.New("device closed the serial port before returning hashes")
		}
	}
}

type rnodeHashSnapshot struct {
	deviceHash         []byte
	firmwareHashTarget []byte
	firmwareHash       []byte
}

func captureRnodeEEPROM(portName string, port serialPort, timeout time.Duration) (*eepromDownloaderState, error) {
	state := newRnodeReadLoopState()

	if _, err := port.Write([]byte{kissFend, rnodeKISSCommandROMRead, 0x00, kissFend}); err != nil {
		_ = port.Close()
		return nil, err
	}

	deadline := time.After(timeout)
	for {
		readCh := make(chan struct {
			b   byte
			n   int
			err error
		}, 1)
		go func() {
			buf := make([]byte, 1)
			n, err := port.Read(buf)
			if n > 0 {
				readCh <- struct {
					b   byte
					n   int
					err error
				}{b: buf[0], n: n, err: err}
				return
			}
			readCh <- struct {
				b   byte
				n   int
				err error
			}{err: err}
		}()

		select {
		case res := <-readCh:
			if res.n > 0 {
				if frame, ok := state.feedByte(res.b); ok && frame.command == rnodeKISSCommandROMRead {
					eepromState := &eepromDownloaderState{name: "rnode", eeprom: append([]byte(nil), frame.payload...)}
					if err := eepromState.parseEEPROM(); err != nil {
						return nil, err
					}
					return eepromState, nil
				}
			}
			if res.err != nil {
				if errors.Is(res.err, io.EOF) {
					_ = port.Close()
					return nil, fmt.Errorf("device %v closed the serial port before returning EEPROM", portName)
				}
				_ = port.Close()
				return nil, res.err
			}
		case <-deadline:
			_ = port.Close()
			return nil, state.eepromTimeoutError()
		}
	}
}

func runFirmwareHashReadbacks(out io.Writer, port string, opts options) error {
	return newRuntime().runFirmwareHashReadbacks(out, port, opts)
}

func (rt cliRuntime) runFirmwareHashReadbacks(out io.Writer, port string, opts options) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	eepromState, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		return err
	}
	if !eepromState.provisioned {
		return errors.New("This device has not been provisioned yet, cannot get firmware hash")
	}

	snapshot, err := captureRnodeHashes(serial, 5*time.Second)
	if err != nil {
		return err
	}

	if opts.getTargetFirmwareHash {
		if _, err := fmt.Fprintf(out, "The target firmware hash is: %x\n", snapshot.firmwareHashTarget); err != nil {
			return err
		}
	}
	if opts.getFirmwareHash {
		if _, err := fmt.Fprintf(out, "The actual firmware hash is: %x\n", snapshot.firmwareHash); err != nil {
			return err
		}
	}
	return nil
}

func runFirmwareHashSet(out io.Writer, port, hashHex string) error {
	return newRuntime().runFirmwareHashSet(out, port, hashHex)
}

func (rt cliRuntime) runFirmwareHashSet(out io.Writer, port, hashHex string) (err error) {
	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil || len(hashBytes) != 32 {
		return errors.New("The provided value was not a valid SHA256 hash")
	}

	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	eepromState, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		return err
	}
	if !eepromState.provisioned {
		return errors.New("This device has not been provisioned yet, cannot set firmware hash")
	}

	state := &firmwareHashSetterState{name: "rnode", hashBytes: hashBytes, writer: serial}
	if err := state.setFirmwareHash(); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Firmware hash set"); err != nil {
		return err
	}
	return nil
}

func runDeviceSigning(out io.Writer, port string) error {
	return newRuntime().runDeviceSigning(out, port)
}

func (rt cliRuntime) runDeviceSigning(out io.Writer, port string) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	eepromState, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		return err
	}
	if !eepromState.provisioned {
		return errors.New("This device has not been provisioned yet, cannot create device signature")
	}

	snapshot, err := captureRnodeHashes(serial, 5*time.Second)
	if err != nil {
		return err
	}
	if len(snapshot.deviceHash) == 0 {
		if _, err := fmt.Fprintln(out, "No device hash present, skipping device signing"); err != nil {
			return err
		}
		return nil
	}

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}
	deviceSigner, err := rns.FromFile(filepath.Join(configDir, "firmware", "device.key"), rt.logger)
	if err != nil {
		if _, writeErr := fmt.Fprintln(out, "Could not load device signing key (did you run \"gornodeconf --key\"?)"); writeErr != nil {
			return writeErr
		}
		return exitCodeError{code: 78, err: fmt.Errorf("No device signer loaded, cannot sign device: %w", err)}
	}

	if deviceSigner == nil {
		if _, err := fmt.Fprintln(out, "No device signer loaded, cannot sign device"); err != nil {
			return err
		}
		return exitCodeError{code: 78, err: errors.New("No device signer loaded, cannot sign device")}
	}

	signature, err := deviceSigner.Sign(snapshot.deviceHash)
	if err != nil {
		return err
	}

	state := &signatureSetterState{name: "rnode", signature: signature, writer: serial}
	if err := state.storeSignature(); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Device signed"); err != nil {
		return err
	}
	return nil
}

func runEEPROMBootstrap(out io.Writer, port string, opts options) error {
	return newRuntime().runEEPROMBootstrap(out, port, opts)
}

func (rt cliRuntime) runEEPROMBootstrap(out io.Writer, port string, opts options) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	eepromState, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		return err
	}
	if eepromState.signatureValid || (eepromState.provisioned && !opts.autoinstall) {
		if _, err := fmt.Fprintln(out, "EEPROM bootstrap was requested, but a valid EEPROM was already present."); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "No changes are being made."); err != nil {
			return err
		}
		return nil
	}

	if opts.autoinstall && eepromState.provisioned {
		platform, err := readRnodePlatform(port, serial, 5*time.Second)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "Clearing old EEPROM, this will take about 15 seconds..."); err != nil {
			return err
		}
		state := &modeSwitchState{platform: platform, writer: serial, sleeper: rt}
		if err := state.wipeEEPROM(); err != nil {
			return err
		}
	}

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}

	identity, err := resolveBootstrapIdentity(opts)
	if err != nil {
		return err
	}

	serialno, err := nextBootstrapSerialNumber(configDir)
	if err != nil {
		return err
	}

	timestamp := uint32(rt.now().Unix())
	checksum := checksumInfoHash(identity.product, identity.model, identity.hwRev, serialno, timestamp)

	loader := rt.loadBootstrapSigner
	if loader == nil {
		loader = loadBootstrapSigner
	}
	signer, err := loader(configDir)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Bootstrapping device EEPROM..."); err != nil {
		return err
	}
	signature, err := signer.Sign(checksum)
	if err != nil {
		return err
	}
	image := bootstrapEEPROMImage(identity.product, identity.model, identity.hwRev, serialno, timestamp, signature)
	if err := bootstrapEEPROM(serial, identity.product, identity.model, identity.hwRev, serialno, timestamp, signature); err != nil {
		return err
	}
	if _, err := writeDeviceIdentityBackup(configDir, serialno, image); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "EEPROM Bootstrapping successful!"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Saved device identity"); err != nil {
		return err
	}
	return nil
}

func bootstrapEEPROM(writer eepromDownloaderWriter, product, model byte, hwRev byte, serialno, timestamp uint32, signature []byte) error {
	infoBytes := [][]byte{{0x00, product}, {0x01, model}, {0x02, hwRev}}
	for _, field := range infoBytes {
		if err := writeEEPROMByte(writer, field[0], field[1]); err != nil {
			return err
		}
	}

	serialBytes := []byte{byte(serialno >> 24), byte(serialno >> 16), byte(serialno >> 8), byte(serialno)}
	for offset, value := range serialBytes {
		if err := writeEEPROMByte(writer, byte(0x03+offset), value); err != nil {
			return err
		}
	}

	timeBytes := []byte{byte(timestamp >> 24), byte(timestamp >> 16), byte(timestamp >> 8), byte(timestamp)}
	for offset, value := range timeBytes {
		if err := writeEEPROMByte(writer, byte(0x07+offset), value); err != nil {
			return err
		}
	}

	checksum := checksumInfoHash(product, model, hwRev, serialno, timestamp)
	for offset, value := range checksum {
		if err := writeEEPROMByte(writer, byte(0x0b+offset), value); err != nil {
			return err
		}
	}

	if err := storeSignature(writer, signature); err != nil {
		return err
	}

	if err := writeEEPROMByte(writer, 0x9b, 0x73); err != nil {
		return err
	}

	return nil
}

func rnodeDetectCommand() []byte {
	return []byte{
		kissFend, 0x08, 0x73, kissFend,
		kissFend, 0x50, 0x00, kissFend,
		kissFend, 0x48, 0x00, kissFend,
		kissFend, 0x49, 0x00, kissFend,
		kissFend, 0x47, 0x00, kissFend,
		kissFend, 0x56, 0x01, kissFend,
		kissFend, 0x60, 0x01, kissFend,
		kissFend, 0x60, 0x02, kissFend,
	}
}

func formatBootstrapEEPROMError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w", err)
}

func writeDeviceIdentityBackup(configDir string, serialno uint32, eeprom []byte) (string, error) {
	backupDir := filepath.Join(configDir, "firmware", "device_db")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	serialBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(serialBytes, serialno)
	path := filepath.Join(backupDir, hex.EncodeToString(serialBytes))
	if err := os.WriteFile(path, append([]byte(nil), eeprom...), 0o644); err != nil {
		return "", fmt.Errorf("could not backup device EEPROM to disk")
	}
	return path, nil
}

func nextBootstrapSerialNumber(configDir string) (uint32, error) {
	counterPath := filepath.Join(configDir, "firmware", "serial.counter")
	var counter uint32
	if data, err := os.ReadFile(counterPath); err == nil {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil {
			return 0, fmt.Errorf("could not create device serial number, exiting")
		}
		counter = uint32(parsed)
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("could not create device serial number, exiting")
	}

	serialno := counter + 1
	if err := os.MkdirAll(filepath.Dir(counterPath), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(counterPath, []byte(strconv.FormatUint(uint64(serialno), 10)), 0o644); err != nil {
		return 0, fmt.Errorf("could not create device serial number, exiting")
	}
	return serialno, nil
}

type bootstrapIdentity struct {
	product byte
	model   byte
	hwRev   byte
}

func resolveBootstrapIdentity(opts options) (bootstrapIdentity, error) {
	identity := bootstrapIdentity{product: 0x03}

	if opts.product != "" {
		value, err := resolveBootstrapByte(opts.product, map[string]byte{"03": 0x03, "10": 0x10, "f0": 0xf0, "e0": 0xe0})
		if err != nil {
			return bootstrapIdentity{}, err
		}
		identity.product = value
	}

	if opts.model == "" {
		return bootstrapIdentity{}, fmt.Errorf("model is required for bootstrap")
	}
	model, err := resolveBootstrapByte(opts.model, map[string]byte{"11": 0x11, "12": 0x12, "a4": 0xa4, "a9": 0xa9, "a1": 0xa1, "a6": 0xa6, "e4": 0xe4, "e9": 0xe9, "ff": 0xff})
	if err != nil {
		return bootstrapIdentity{}, err
	}
	identity.model = model

	if opts.hwrev < 1 || opts.hwrev > 255 {
		return bootstrapIdentity{}, fmt.Errorf("hardware revision is required for bootstrap")
	}
	identity.hwRev = byte(opts.hwrev)

	return identity, nil
}

func resolveBootstrapByte(value string, aliases map[string]byte) (byte, error) {
	if mapped, ok := aliases[value]; ok {
		return mapped, nil
	}
	if len(value) == 2 {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 1 {
			return 0, fmt.Errorf("invalid bootstrap value %q", value)
		}
		return decoded[0], nil
	}
	parsed, err := strconv.ParseUint(value, 10, 8)
	if err == nil {
		return byte(parsed), nil
	}
	return 0, fmt.Errorf("invalid bootstrap value %q", value)
}

func checksumInfoHash(product, model, hwRev byte, serialno, timestamp uint32) []byte {
	infoChunk := []byte{product, model, hwRev}
	serialBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(serialBytes, serialno)
	timeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(timeBytes, timestamp)
	infoChunk = append(infoChunk, serialBytes...)
	infoChunk = append(infoChunk, timeBytes...)
	digest := md5.Sum(infoChunk)
	return append([]byte(nil), digest[:]...)
}

func bootstrapEEPROMImage(product, model byte, hwRev byte, serialno, timestamp uint32, signature []byte) []byte {
	image := make([]byte, 0xa8)
	image[0x00] = product
	image[0x01] = model
	image[0x02] = hwRev
	image[0x03] = byte(serialno >> 24)
	image[0x04] = byte(serialno >> 16)
	image[0x05] = byte(serialno >> 8)
	image[0x06] = byte(serialno)
	image[0x07] = byte(timestamp >> 24)
	image[0x08] = byte(timestamp >> 16)
	image[0x09] = byte(timestamp >> 8)
	image[0x0a] = byte(timestamp)
	checksum := checksumInfoHash(product, model, hwRev, serialno, timestamp)
	copy(image[0x0b:0x1b], checksum)
	copy(image[0x1b:0x1b+len(signature)], signature)
	image[0x9b] = 0x73
	return image
}

func writeEEPROMByte(writer eepromDownloaderWriter, addr, value byte) error {
	command := []byte{kissFend, rnodeKISSCommandROMWrite, addr, value, kissFend}
	written, err := writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return fmt.Errorf("An IO error occurred while writing EEPROM")
	}
	return nil
}

func readRnodePlatform(portName string, port serialPort, timeout time.Duration) (byte, error) {
	_ = portName
	state := newRnodeReadLoopState()

	if _, err := port.Write([]byte{kissFend, rnodeKISSCommandPlatform, 0x00, kissFend}); err != nil {
		_ = port.Close()
		return romPlatformAVR, err
	}

	deadline := time.After(timeout)
	for {
		readCh := make(chan struct {
			b   byte
			n   int
			err error
		}, 1)
		go func() {
			buf := make([]byte, 1)
			n, err := port.Read(buf)
			if n > 0 {
				readCh <- struct {
					b   byte
					n   int
					err error
				}{b: buf[0], n: n, err: err}
				return
			}
			readCh <- struct {
				b   byte
				n   int
				err error
			}{err: err}
		}()

		select {
		case res := <-readCh:
			if res.n > 0 {
				state.feedByte(res.b)
				if state.platform != 0 {
					return state.platform, nil
				}
			}
			if res.err != nil {
				return romPlatformAVR, nil
			}
		case <-deadline:
			_ = port.Close()
			return romPlatformAVR, nil
		}
	}
}
