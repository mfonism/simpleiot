package network

// this module currently supports the BG96 modem connected via USB

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"

	nmea "github.com/adrianmo/go-nmea"
	"github.com/jacobsa/go-serial/serial"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/file"
	"github.com/simpleiot/simpleiot/respreader"
)

// APNVerizon is the APN to use on VZ network
const APNVerizon = "vzwinternet"

// APNKajeet is the APN to use on the Kajeet network
const APNKajeet = "kajeet.gw12.vzwentp"

// APNHologram is the APN to use on the Hologram network
const APNHologram = "hologram"

// Modem is an interface that always reports detected/connected
type Modem struct {
	iface      string
	atCmdPort  io.ReadWriteCloser
	lastPPPRun time.Time
	config     ModemConfig
	enabled    bool
}

// ModemConfig describes the configuration for a modem
type ModemConfig struct {
	ChatScript    string
	AtCmdPortName string
	Reset         func() error
	Debug         bool
	APN           string
}

// NewModem constructor
func NewModem(config ModemConfig) *Modem {
	ret := &Modem{
		iface:  "ppp0",
		config: config,
	}

	DebugAtCommands = config.Debug

	return ret
}

func (m *Modem) openCmdPort() error {
	if m.atCmdPort != nil {
		// port is already open
		return nil
	}

	if !m.detected() {
		return errors.New("open failed, modem not detected")
	}

	options := serial.OpenOptions{
		PortName:          m.config.AtCmdPortName,
		BaudRate:          115200,
		DataBits:          8,
		StopBits:          1,
		MinimumReadSize:   1,
		RTSCTSFlowControl: true,
	}

	port, err := serial.Open(options)

	if err != nil {
		return err
	}

	m.atCmdPort = respreader.NewReadWriteCloser(port, 10*time.Second,
		50*time.Millisecond)

	return nil
}

// Desc returns description
func (m *Modem) Desc() string {
	return "modem"
}

// detected returns true if modem detected
func (m *Modem) detected() bool {
	return file.Exists("/dev/ttyUSB0") &&
		file.Exists("/dev/ttyUSB1") &&
		file.Exists("/dev/ttyUSB2") &&
		file.Exists("/dev/ttyUSB3")
}

func (m *Modem) pppActive() bool {
	if !m.detected() {
		return false
	}

	_, err := GetIP(m.iface)
	return err == nil
}

// Configure modem interface
func (m *Modem) Configure() (InterfaceConfig, error) {
	if !m.enabled {
		return InterfaceConfig{}, errors.New("Configure error, modem disabled")
	}

	ret := InterfaceConfig{
		Apn: m.config.APN,
	}

	// current sets APN and configures for internal SIM
	if err := m.openCmdPort(); err != nil {
		return ret, err
	}

	// disable echo as it messes up the respreader in that it
	// echos the command, which is not part of the response

	err := CmdOK(m.atCmdPort, "ATE0")
	if err != nil {
		return ret, err
	}

	err = CmdSetApn(m.atCmdPort, m.config.APN)
	if err != nil {
		return ret, err
	}

	mode, err := CmdBg96GetScanMode(m.atCmdPort)
	fmt.Println("BG96 scan mode: ", mode)
	if err != nil {
		return ret, fmt.Errorf("Error getting scan mode: %v", err.Error())
	}

	if mode != BG96ScanModeLTE {
		fmt.Println("Setting BG96 scan mode ...")
		err := CmdBg96ForceLTE(m.atCmdPort)
		if err != nil {
			return ret, fmt.Errorf("Error setting scan mode: %v", err.Error())
		}
	}

	err = CmdFunMin(m.atCmdPort)
	if err != nil {
		return ret, fmt.Errorf("Error setting fun Min: %v", err.Error())
	}

	err = CmdOK(m.atCmdPort, "AT+QCFG=\"gpio\",1,26,1,0,0,1")
	if err != nil {
		return ret, fmt.Errorf("Error setting GPIO: %v", err.Error())
	}

	// VZ and Kajeet can use internal VZ SIM, Hologram needs external SIM
	if m.config.APN == APNVerizon || m.config.APN == APNKajeet {
		err = CmdOK(m.atCmdPort, "AT+QCFG=\"gpio\",3,26,1,1")
		if err != nil {
			return ret, fmt.Errorf("Error setting GPIO: %v", err.Error())
		}

	} else {
		err = CmdOK(m.atCmdPort, "AT+QCFG=\"gpio\",3,26,0,1")
		if err != nil {
			return ret, fmt.Errorf("Error setting GPIO: %v", err.Error())
		}

	}

	err = CmdFunFull(m.atCmdPort)
	if err != nil {
		return ret, fmt.Errorf("Error setting fun full: %v", err.Error())
	}

	// enable GPS. Don't return error of GPS commands fail as
	// this is not a critical error
	err = CmdOK(m.atCmdPort, "AT+QGPS=1")
	if err != nil {
		log.Printf("Error enabling GPS: %v", err.Error())
	}

	err = CmdOK(m.atCmdPort, "AT+QGPSCFG=\"nmeasrc\",1")
	if err != nil {
		log.Printf("Error settings GPS source: %v", err.Error())
	}

	sim, err := CmdGetSimBg96(m.atCmdPort)

	if err != nil {
		return ret, fmt.Errorf("Error getting SIM #: %v", err.Error())
	}

	ret.Sim = sim

	imei, err := CmdGetImei(m.atCmdPort)

	if err != nil {
		return ret, fmt.Errorf("Error getting IMEI #: %v", err.Error())
	}

	ret.Imei = imei

	version, err := CmdGetFwVersionBG96(m.atCmdPort)

	if err != nil {
		return ret, fmt.Errorf("Error getting fw version #: %v", err.Error())
	}

	ret.Version = version

	return ret, nil
}

// Connect stub
func (m *Modem) Connect() error {
	if !m.enabled {
		return errors.New("Connect error, modem disabled")
	}

	if err := m.openCmdPort(); err != nil {
		return err
	}

	mode, err := CmdBg96GetScanMode(m.atCmdPort)

	if err != nil {
		return err
	}

	log.Println("BG96 scan mode: ", mode)

	if mode != BG96ScanModeLTE {
		log.Println("Setting BG96 scan mode")
		err := CmdBg96ForceLTE(m.atCmdPort)
		if err != nil {
			return err
		}
	}

	/*
		service, _, _, _, err := CmdQcsq(m.atCmdPort)
		if err != nil {
			return err
		}

		// TODO need to set APN, etc before we do this
		// but eventually want to make sure we have service
		// before running PPP
		if !service {

		}
	*/

	if time.Since(m.lastPPPRun) < 30*time.Second {
		return errors.New("only run PPP once every 30s")
	}

	m.lastPPPRun = time.Now()

	log.Println("Modem: starting PPP")
	return exec.Command("pon", m.config.ChatScript).Run()
}

// GetStatus return interface status
func (m *Modem) GetStatus() (InterfaceStatus, error) {
	if !m.detected() || !m.enabled {
		return InterfaceStatus{}, nil
	}

	if err := m.openCmdPort(); err != nil {
		return InterfaceStatus{}, err
	}

	var retError error
	ip, _ := GetIP(m.iface)

	service, rssi, rsrp, rsrq, err := CmdQcsq(m.atCmdPort)
	if err != nil {
		retError = err
	}

	var network string

	if service {
		network, err = CmdCops(m.atCmdPort)
		if err != nil {
			retError = err
		}
	}

	return InterfaceStatus{
		Detected:  m.detected(),
		Connected: m.pppActive() && service,
		Operator:  network,
		IP:        ip,
		Signal:    rssi,
		Rsrp:      rsrp,
		Rsrq:      rsrq,
	}, retError
}

// Reset stub
func (m *Modem) Reset() error {
	if m.atCmdPort != nil {
		m.atCmdPort.Close()
		m.atCmdPort = nil
	}

	err := exec.Command("poff").Run()
	if err != nil {
		log.Println("poff exec error: ", err)
	}
	if m.enabled {
		err := m.config.Reset()
		if err != nil {
			return err
		}
	}

	return nil
}

// Enable or disable interface
func (m *Modem) Enable(en bool) error {
	log.Println("Modem enable: ", en)
	var err error
	m.enabled = en
	if err = m.openCmdPort(); err != nil {
		return err
	}

	if en {
		err = CmdFunFull(m.atCmdPort)
		if err != nil {
			return err
		}
	} else {
		err = CmdFunMin(m.atCmdPort)
		if err != nil {
			return err
		}
	}

	return nil
}

// ErrorModemNotDetected is returned if we try an operation and the modem
// is not detected
var ErrorModemNotDetected = errors.New("No modem detected")

// GetLocation returns current GPS location
func (m *Modem) GetLocation() (data.GpsPos, error) {
	if !m.detected() {
		return data.GpsPos{}, ErrorModemNotDetected
	}

	if err := m.openCmdPort(); err != nil {
		return data.GpsPos{}, err
	}

	line, err := CmdGGA(m.atCmdPort)

	if err != nil {
		return data.GpsPos{}, err
	}

	s, err := nmea.Parse(strings.TrimSpace(line))
	if err != nil {
		return data.GpsPos{}, err
	}

	if s.DataType() != nmea.TypeGGA {
		return data.GpsPos{}, errors.New("GPS not GGA response")
	}

	gga := s.(nmea.GGA)
	ret := data.GpsPos{}
	ret.FromGPGGA(gga)
	return ret, nil
}
