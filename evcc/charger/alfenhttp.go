package charger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/cmd/shutdown"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/request"
	"github.com/evcc-io/evcc/util/transport"
)

type AlfenHttp struct {
	*request.Helper
	log            *util.Logger
	uri            string
	password       string
	mu             sync.Mutex
	getPropertiesG func() (*Properties, error)
}

func init() {
	registry.Add("alfenhttp", NewAlfenHttpFromConfig)
}

func NewAlfenHttpFromConfig(other map[string]interface{}) (api.Charger, error) {
	var cc struct {
		Uri      string
		Password string
	}

	if err := util.DecodeOther(other, &cc); err != nil {
		return nil, err
	}

	c, err := NewAlfenHttp(util.DefaultScheme(cc.Uri, "https"), cc.Password)

	return c, err
}

func NewAlfenHttp(uri string, password string) (*AlfenHttp, error) {
	log := util.NewLogger("alfenhttp").Redact(password)

	c := &AlfenHttp{
		Helper:   newInsecureHelper(log),
		log:      log,
		uri:      strings.TrimRight(uri, "/"),
		password: password,
	}

	c.getPropertiesG = util.Cached(func() (*Properties, error) {
		return c.getProperties()
	}, time.Second*5)

	shutdown.Register(c.Shutdown)

	if err := c.login(); err != nil {
		return nil, err
	}

	return c, nil
}

// see https://github.com/leeyuentuen/alfen_wallbox/blob/master/custom_components/alfen_wallbox/sensor.py
// and https://github.com/leeyuentuen/alfen_wallbox/blob/master/custom_components/alfen_wallbox/number.py
const (
	available              = 4
	cableConnected         = 7
	evConnected            = 8
	preparingCharging      = 9
	waitVehicleCharging    = 10
	chargingNormal         = 11
	finishWaitVehicle      = 16
	finishWaitDisconnect   = 17
	errorCharging          = 21
	errorTooManyRestarts   = 26
	inoperative            = 34
	loadBalancingForcedOff = 36
)

const (
	bootups                    = "2056_0"
	bootReason                 = "2057_0"
	uptime                     = "2060_0"
	maxStationCurrent          = "2062_0"
	voltageL1Socket1           = "2221_3"
	voltageL2Socket1           = "2221_4"
	voltageL3Socket1           = "2221_5"
	activePowerTotal           = "2221_16"
	meterReadingSocket1        = "2221_22"
	currentL1Socket1           = "2221_A"
	currentL2Socket1           = "2221_B"
	currentL3Socket1           = "2221_C"
	state                      = "2501_2"
	connector1MaxAllowedPhases = "312E_0"
)

var readProps = []string{
	bootups,
	bootReason,
	uptime,
	maxStationCurrent,
	voltageL1Socket1,
	voltageL2Socket1,
	voltageL3Socket1,
	activePowerTotal,
	meterReadingSocket1,
	currentL1Socket1,
	currentL2Socket1,
	currentL3Socket1,
	state,
	connector1MaxAllowedPhases,
}

const (
	loadBalancingEnablePhaseSwitching = "2185_0"
	installationMaxAllowedPhases      = "2189_0"
)

const statusOn = "1"

const (
	switchOffCurrent int64 = 5
	maxCurrent       int64 = 16
)

const alfenContentType = "alfen/json; charset=utf-8"

func (c *AlfenHttp) Shutdown() {
	c.log.DEBUG.Print("resetting charger to 3p")
	c.Phases1p3p(3)
	c.log.DEBUG.Printf("resetting charger current to %dA", maxCurrent)
	c.MaxCurrent(maxCurrent)

	c.logout()
}

var _ api.Charger = (*AlfenHttp)(nil)

func (c *AlfenHttp) Status() (api.ChargeStatus, error) {
	value, err := c.getProperty(state)

	status := api.StatusNone
	if err == nil {
		switch value.(float64) {
		case available:
			status = api.StatusA
		case cableConnected,
			evConnected,
			preparingCharging,
			waitVehicleCharging,
			finishWaitVehicle,
			finishWaitDisconnect,
			loadBalancingForcedOff:
			status = api.StatusB
		case chargingNormal:
			status = api.StatusC
		case errorCharging,
			errorTooManyRestarts,
			inoperative:
			status = api.StatusE
		default:
			err = fmt.Errorf("unhandled status: %s", fmt.Sprint(value))
		}
	}

	c.log.TRACE.Printf("mapped value %s to status %s", fmt.Sprint(value), status)

	return status, err
}

func (c *AlfenHttp) Enabled() (bool, error) {
	maxCurrent, err := c.getProperty(maxStationCurrent)

	if err != nil {
		return false, err
	}

	return (maxCurrent.(float64) > float64(switchOffCurrent)), nil
}

func (c *AlfenHttp) Enable(enable bool) error {
	if enable {
		return c.MaxCurrent(maxCurrent)
	} else {
		return c.MaxCurrent(switchOffCurrent)
	}
}

func (c *AlfenHttp) MaxCurrent(current int64) error {
	return c.setProperty(maxStationCurrent, fmt.Sprint(current))
}

var _ api.CurrentGetter = (*AlfenHttp)(nil)

func (c *AlfenHttp) GetMaxCurrent() (float64, error) {
	value, err := c.getProperty(maxStationCurrent)

	if err != nil {
		return 0, err
	}

	return value.(float64), err
}

var _ api.PhaseGetter = (*AlfenHttp)(nil)

func (c *AlfenHttp) GetPhases() (int, error) {
	value, err := c.getProperty(connector1MaxAllowedPhases)

	if err != nil {
		return 0, err
	}

	return int(value.(float64)), err
}

var _ api.Meter = (*AlfenHttp)(nil)

func (c *AlfenHttp) CurrentPower() (float64, error) {
	value, err := c.getProperty(activePowerTotal)

	if err != nil {
		return 0, err
	}

	return value.(float64), err
}

var _ api.PhaseSwitcher = (*AlfenHttp)(nil)

func (c *AlfenHttp) Phases1p3p(phases int) error {
	err := c.setProperty(loadBalancingEnablePhaseSwitching, statusOn)

	if err != nil {
		return err
	}

	return c.setProperty(installationMaxAllowedPhases, fmt.Sprint(phases))
}

var _ api.MeterEnergy = (*AlfenHttp)(nil)

func (c *AlfenHttp) TotalEnergy() (float64, error) {
	totalEnergy, err := c.getProperty(meterReadingSocket1)

	if err != nil {
		return 0, err
	}

	return (totalEnergy.(float64) / 1000), nil
}

var _ api.PhaseCurrents = (*AlfenHttp)(nil)

func (c *AlfenHttp) Currents() (float64, float64, float64, error) {
	currentL1, err := c.getProperty(currentL1Socket1)

	if err != nil {
		return 0, 0, 0, err
	}

	currentL2, err := c.getProperty(currentL2Socket1)

	if err != nil {
		return 0, 0, 0, err
	}

	currentL3, err := c.getProperty(currentL3Socket1)

	if err != nil {
		return 0, 0, 0, err
	}

	return currentL1.(float64), currentL2.(float64), currentL3.(float64), nil
}

var _ api.PhaseVoltages = (*AlfenHttp)(nil)

func (c *AlfenHttp) Voltages() (float64, float64, float64, error) {
	voltageL1, err := c.getProperty(voltageL1Socket1)

	if err != nil {
		return 0, 0, 0, err
	}

	voltageL2, err := c.getProperty(voltageL2Socket1)

	if err != nil {
		return 0, 0, 0, err
	}

	voltageL3, err := c.getProperty(voltageL3Socket1)

	if err != nil {
		return 0, 0, 0, err
	}

	return voltageL1.(float64), voltageL2.(float64), voltageL3.(float64), nil
}

var _ api.Diagnosis = (*AlfenHttp)(nil)

func (c *AlfenHttp) Diagnose() {
	info, err := c.getInfo()
	if err != nil {
		return
	}
	fmt.Printf("Model:  \t%s\n", info.Model)
	fmt.Printf("Firmware:\t%s\n", info.FwVersion)
	fmt.Printf("Model ID:\t%s\n", info.Model)
	fmt.Printf("Object ID:\t%s\n", info.ObjectId)

	if bootups, err := c.getProperty(bootups); err == nil {
		fmt.Printf("Bootups:\t%d\n", int(bootups.(float64)))
	}
	if bootReason, err := c.getProperty(bootReason); err == nil {
		fmt.Printf("Bootup reason:\t%s\n", bootReason.(string))
	}
	if uptime, err := c.getProperty(uptime); err == nil {
		// keep blank for proper tabular formatting
		fmt.Printf("Uptime: \t%d\n", int(uptime.(float64)))
	}
}

func newInsecureClient(log *util.Logger) *http.Client {
	tr := transport.Insecure()

	return &http.Client{
		Timeout:   request.Timeout,
		Transport: request.NewTripper(log, tr),
	}
}

func newInsecureHelper(log *util.Logger) *request.Helper {
	return &request.Helper{
		Client: newInsecureClient(log),
	}
}

func (c *AlfenHttp) ensureAuthenticated(method func() (resp *http.Response, err error)) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := method()

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		// re-authenticate amd retry in case of a logout
		c.log.WARN.Printf("Session no longer valid - re-authenticating")

		if err := c.login(); err != nil {
			return nil, err
		}

		resp, err = method()

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()
	}

	return resp, err
}

type Login struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Property struct {
	Id     string      `json:"id"`
	Access int         `json:"access"`
	Type   int         `json:"type"`
	Len    int         `json:"len"`
	Cat    string      `json:"cat"`
	Value  interface{} `json:"value"`
}

type Properties struct {
	Version    int        `json:"version"`
	Properties []Property `json:"properties"`
	Offset     int        `json:"offset"`
	Total      int        `json:"total"`
}

type PropertyRequest struct {
	Id    string `json:"id"`
	Value string `json:"value"`
}

type Info struct {
	Identity   string `json:"Identity"`
	ScnNetwork string `json:"SCNNetwork"`
	FwVersion  string `json:"FWVersion"`
	LastConfig int64  `json:"LastConfig"`
	Model      string `json:"Model"`
	ObjectId   string `json:"ObjectId"`
	Type       string `json:"Type"`
}

func (c *AlfenHttp) processResponse(resp *http.Response, err error) (string, error) {
	if err != nil {
		return "", err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body[:]), nil
}

func (c *AlfenHttp) login() error {
	c.log.TRACE.Println("start of login")
	defer c.log.TRACE.Println("end of login")

	payload := new(bytes.Buffer)
	encoder := json.NewEncoder(payload)
	encoder.SetEscapeHTML(false)

	login := Login{
		Username: "admin",
		Password: c.password,
	}
	encoder.Encode(&login)

	resp, err := c.Post(c.uri+"/api/login", alfenContentType, payload)

	if err != nil {
		c.log.DEBUG.Printf("error during login: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("login failed: %v", resp.Status)
	}

	_, err = c.processResponse(resp, err)

	return err
}

func (c *AlfenHttp) logout() error {
	c.log.TRACE.Println("start of logout")
	defer c.log.TRACE.Println("end of logout")

	resp, err := c.Post(c.uri+"/api/logout", alfenContentType, nil)

	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("logout failed: %v", resp.Status)
	}

	_, err = c.processResponse(resp, err)

	return err
}

func (c *AlfenHttp) getProperties() (*Properties, error) {
	c.log.TRACE.Printf("start of getProperties")
	defer c.log.TRACE.Println("end of getProperties")

	resp, err := c.ensureAuthenticated(func() (*http.Response, error) {
		return c.Get(c.uri + "/api/prop?ids=" + strings.Join(readProps[:], ","))
	})

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error getting properties: %v", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var props Properties
	err = json.Unmarshal(body, &props)
	if err != nil {
		return nil, err
	}

	return &props, err
}

func (c *AlfenHttp) getProperty(id string) (interface{}, error) {
	props, err := c.getPropertiesG()

	if err != nil {
		return "", err
	}

	for _, prop := range props.Properties {
		if prop.Id == id {
			return prop.Value, nil
		}
	}

	return "", fmt.Errorf("unable to get property %s from %v", id, props)
}

func (c *AlfenHttp) setProperty(property string, value string) error {
	c.log.TRACE.Printf("start of setProperty %s to %s", property, value)
	defer c.log.TRACE.Printf("end of setProperty %s to %s", property, value)

	data := make(map[string]PropertyRequest)
	data[property] = PropertyRequest{
		Id:    property,
		Value: value,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	resp, err := c.ensureAuthenticated(func() (*http.Response, error) {
		return c.Post(c.uri+"/api/prop", alfenContentType, bytes.NewReader(jsonData))
	})

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	return nil
}

func (c *AlfenHttp) getInfo() (*Info, error) {
	c.log.TRACE.Printf("start of getInfo")
	defer c.log.TRACE.Println("end of getInfo")

	resp, err := c.ensureAuthenticated(func() (*http.Response, error) {
		return c.Get(c.uri + "/api/info")
	})

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error getting info: %v", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var info Info
	err = json.Unmarshal(body, &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}
