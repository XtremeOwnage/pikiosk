// Package mqtt exposes the kiosk over MQTT, with Home Assistant device
// discovery. Every action available in the web UI's control page has a
// command topic.
package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/XtremeOwnage/pikiosk/internal/config"
	"github.com/XtremeOwnage/pikiosk/internal/kiosk"
	"github.com/XtremeOwnage/pikiosk/internal/system"
)

const (
	payloadOnline  = "online"
	payloadOffline = "offline"
)

// Command is the payload of <base>/cmd, for automations that need
// parameters such as a duration.
type Command struct {
	Action   string  `json:"action"`
	Name     string  `json:"name,omitempty"`
	URL      string  `json:"url,omitempty"`
	Path     string  `json:"path,omitempty"`
	Value    *int    `json:"value,omitempty"`
	On       *bool   `json:"on,omitempty"`
	Duration float64 `json:"duration,omitempty"` // seconds
	Volume   *int    `json:"volume,omitempty"`   // per-sound volume 0-100
}

// Actions lists every command action, for documentation and the UI.
var Actions = []string{
	"view", "url", "stream", "stop_stream", "reload", "restart_browser",
	"rotation", "resume_rotation", "stop_rotation", "next",
	"play_sound", "stop_sound", "volume", "list_sounds",
	"screen", "wake", "brightness", "reboot",
}

type Bridge struct {
	cfg     *config.Store
	kiosk   *kiosk.Kiosk
	sounds  func() []string
	stats   func() map[string]any
	version string
	webURL  func() string
	log     *slog.Logger

	mu     sync.Mutex
	client paho.Client
	conf   config.Config
	status string
}

// New creates the bridge. sounds lists the sound library; stats returns
// device health for the state message.
func New(cfg *config.Store, k *kiosk.Kiosk, sounds func() []string, stats func() map[string]any, version string, webURL func() string, log *slog.Logger) *Bridge {
	return &Bridge{cfg: cfg, kiosk: k, sounds: sounds, stats: stats, version: version, webURL: webURL, log: log.With("component", "mqtt"), status: "disabled"}
}

func (b *Bridge) Status() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

func (b *Bridge) Run(ctx context.Context) {
	b.cfg.Subscribe(func(old, cfg config.Config) {
		if old.MQTT != cfg.MQTT || old.DeviceID != cfg.DeviceID {
			b.reconnect(cfg)
			return
		}
		if old.DeviceName != cfg.DeviceName ||
			strings.Join(old.ViewNames(), "\x00") != strings.Join(cfg.ViewNames(), "\x00") ||
			strings.Join(old.RotationNames(), "\x00") != strings.Join(cfg.RotationNames(), "\x00") {
			b.mu.Lock()
			b.conf = cfg
			c := b.client
			b.mu.Unlock()
			if c != nil && c.IsConnected() {
				b.publishDiscovery(c, cfg)
			}
		}
	})
	b.kiosk.Subscribe(func(st kiosk.State) { b.publishState(st) })
	b.kiosk.SubscribeEvents(b.publishEvent)
	b.reconnect(b.cfg.Get())

	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			b.disconnect()
			return
		case <-t.C:
			b.publishState(b.kiosk.State())
		}
	}
}

func (b *Bridge) disconnect() {
	b.mu.Lock()
	c := b.client
	conf := b.conf
	b.client = nil
	b.mu.Unlock()
	if c != nil {
		if c.IsConnected() {
			c.Publish(conf.MQTT.BaseTopic+"/availability", 1, true, payloadOffline).WaitTimeout(2 * time.Second)
		}
		c.Disconnect(500)
	}
}

func (b *Bridge) reconnect(cfg config.Config) {
	b.disconnect()
	b.mu.Lock()
	b.conf = cfg
	if !cfg.MQTT.Enabled {
		b.status = "disabled"
		b.mu.Unlock()
		return
	}
	b.status = "connecting"
	b.mu.Unlock()

	m := cfg.MQTT
	base := m.BaseTopic
	scheme := "tcp"
	if m.TLS {
		scheme = "ssl"
	}
	opts := paho.NewClientOptions().
		AddBroker(fmt.Sprintf("%s://%s:%d", scheme, m.Host, m.Port)).
		SetClientID("pikiosk-"+cfg.DeviceID).
		SetUsername(m.Username).
		SetPassword(m.Password).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10*time.Second).
		SetMaxReconnectInterval(time.Minute).
		SetKeepAlive(30*time.Second).
		SetWill(base+"/availability", payloadOffline, 1, true)
	if m.TLS {
		opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: m.TLSInsecure, MinVersion: tls.VersionTLS12})
	}
	opts.SetOnConnectHandler(func(c paho.Client) {
		b.mu.Lock()
		b.status = "connected"
		conf := b.conf
		b.mu.Unlock()
		b.log.Info("connected", "broker", m.Host)
		c.Subscribe(base+"/cmd/#", 1, b.handle)
		c.Subscribe(base+"/cmd", 1, b.handle)
		c.Subscribe(conf.MQTT.DiscoveryPrefix+"/status", 1, func(c paho.Client, msg paho.Message) {
			if string(msg.Payload()) == payloadOnline {
				b.mu.Lock()
				conf := b.conf
				b.mu.Unlock()
				b.publishDiscovery(c, conf)
				b.publishState(b.kiosk.State())
			}
		})
		c.Publish(base+"/availability", 1, true, payloadOnline)
		b.publishDiscovery(c, conf)
		b.publishState(b.kiosk.State())
		b.PublishSounds()
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		b.mu.Lock()
		b.status = "disconnected: " + err.Error()
		b.mu.Unlock()
		b.log.Warn("connection lost", "err", err)
	})

	c := paho.NewClient(opts)
	b.mu.Lock()
	b.client = c
	b.mu.Unlock()
	c.Connect() // retries in the background
}

func (b *Bridge) handle(_ paho.Client, msg paho.Message) {
	b.mu.Lock()
	base := b.conf.MQTT.BaseTopic
	b.mu.Unlock()
	sub := strings.TrimPrefix(strings.TrimPrefix(msg.Topic(), base+"/cmd"), "/")
	payload := strings.TrimSpace(string(msg.Payload()))

	var cmd Command
	if sub == "" {
		if err := json.Unmarshal(msg.Payload(), &cmd); err != nil {
			b.log.Warn("bad command payload", "err", err)
			return
		}
	} else {
		cmd = simpleCommand(sub, payload)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if cmd.Action == "list_sounds" {
		b.PublishSounds()
		return
	}
	if err := b.Execute(ctx, cmd); err != nil {
		b.log.Warn("command failed", "action", cmd.Action, "err", err)
		b.kiosk.Emit("error", cmd.Action+": "+err.Error())
		b.publishError(err)
	}
}

// PublishSounds publishes the sound library, retained, to <base>/sounds.
func (b *Bridge) PublishSounds() {
	b.mu.Lock()
	c := b.client
	base := b.conf.MQTT.BaseTopic
	b.mu.Unlock()
	if c == nil || !c.IsConnected() {
		return
	}
	names := b.sounds()
	if names == nil {
		names = []string{}
	}
	data, _ := json.Marshal(names)
	c.Publish(base+"/sounds", 1, true, data)
}

func (b *Bridge) publishEvent(ev kiosk.Event) {
	b.mu.Lock()
	c := b.client
	base := b.conf.MQTT.BaseTopic
	b.mu.Unlock()
	if c == nil || !c.IsConnected() {
		return
	}
	data, _ := json.Marshal(ev)
	c.Publish(base+"/event", 0, false, data)
}

func simpleCommand(sub, payload string) Command {
	cmd := Command{Action: sub}
	switch sub {
	case "view", "rotation", "enable_view", "disable_view":
		cmd.Name = payload
	case "url", "stream":
		cmd.URL = payload
	case "play_sound":
		cmd.Path = payload
	case "screen":
		on := strings.EqualFold(payload, "ON") || payload == "1" || strings.EqualFold(payload, "true")
		cmd.On = &on
	case "brightness", "volume":
		if v, err := strconv.Atoi(payload); err == nil {
			cmd.Value = &v
		} else if f, err := strconv.ParseFloat(payload, 64); err == nil {
			v := int(f)
			cmd.Value = &v
		}
	}
	return cmd
}

// Execute runs a command; the REST API shares it.
func (b *Bridge) Execute(ctx context.Context, cmd Command) error {
	return Execute(ctx, b.kiosk, cmd)
}

func Execute(ctx context.Context, k *kiosk.Kiosk, cmd Command) error {
	d := time.Duration(cmd.Duration * float64(time.Second))
	switch cmd.Action {
	case "view":
		return k.ShowView(ctx, cmd.Name, d)
	case "url":
		return k.ShowURL(ctx, cmd.URL, d)
	case "stream":
		return k.PlayStream(ctx, cmd.URL, d)
	case "stop_stream":
		return k.StopStream(ctx)
	case "reload":
		return k.Reload(ctx)
	case "restart_browser":
		k.RestartBrowser()
		return nil
	case "screen":
		if cmd.On == nil {
			return fmt.Errorf("screen needs on")
		}
		return k.SetScreen(ctx, *cmd.On)
	case "wake":
		return k.SetScreen(ctx, true)
	case "brightness":
		if cmd.Value == nil {
			return fmt.Errorf("brightness needs value")
		}
		return k.SetBrightness(ctx, *cmd.Value)
	case "enable_view":
		return k.SetViewEnabled(cmd.Name, true)
	case "disable_view":
		return k.SetViewEnabled(cmd.Name, false)
	case "rotation":
		return k.StartRotation(ctx, cmd.Name)
	case "resume_rotation":
		return k.ResumeRotation(ctx)
	case "stop_rotation":
		k.StopRotation()
		return nil
	case "next":
		return k.NextInRotation(ctx)
	case "play_sound":
		ref := cmd.Path
		if ref == "" {
			ref = cmd.URL
		}
		if ref == "" {
			ref = cmd.Name
		}
		vol := -1
		if cmd.Volume != nil {
			vol = *cmd.Volume
		}
		return k.PlaySound(ctx, ref, vol)
	case "stop_sound":
		k.StopSound()
		return nil
	case "volume":
		if cmd.Value == nil {
			return fmt.Errorf("volume needs value")
		}
		return k.SetVolume(ctx, *cmd.Value)
	case "reboot":
		k.Emit("reboot", "requested")
		return system.Reboot(ctx)
	default:
		return fmt.Errorf("unknown action %q", cmd.Action)
	}
}

type stateMsg struct {
	kiosk.State
	Online bool           `json:"online"`
	Device map[string]any `json:"device,omitempty"`
}

func (b *Bridge) publishState(st kiosk.State) {
	b.mu.Lock()
	c := b.client
	base := b.conf.MQTT.BaseTopic
	b.mu.Unlock()
	if c == nil || !c.IsConnected() {
		return
	}
	data, _ := json.Marshal(stateMsg{State: st, Online: true, Device: b.stats()})
	c.Publish(base+"/state", 1, true, data)
}

func (b *Bridge) publishError(err error) {
	b.mu.Lock()
	c := b.client
	base := b.conf.MQTT.BaseTopic
	b.mu.Unlock()
	if c == nil || !c.IsConnected() {
		return
	}
	c.Publish(base+"/error", 1, false, err.Error())
}

// DiscoveryTopic is where the retained Home Assistant device-discovery
// message lives.
func DiscoveryTopic(cfg config.Config) string {
	return fmt.Sprintf("%s/device/%s/config", cfg.MQTT.DiscoveryPrefix, cfg.DeviceID)
}

// publishDiscovery sends one Home Assistant device-discovery message
// describing every entity. It does not wait, so it is safe in callbacks.
func (b *Bridge) publishDiscovery(c paho.Client, cfg config.Config) paho.Token {
	if !cfg.MQTT.Discovery {
		return nil
	}
	data, err := json.Marshal(Discovery(cfg, b.version, b.webURL()))
	if err != nil {
		return nil
	}
	topic := DiscoveryTopic(cfg)
	t := c.Publish(topic, 1, true, data)
	go func() {
		if !t.WaitTimeout(10*time.Second) || t.Error() != nil {
			b.log.Warn("discovery publish failed", "topic", topic, "err", t.Error())
			return
		}
		b.log.Info("discovery published", "topic", topic, "bytes", len(data))
	}()
	return t
}

func (b *Bridge) connected() (paho.Client, config.Config, error) {
	b.mu.Lock()
	c, conf := b.client, b.conf
	b.mu.Unlock()
	if c == nil || !c.IsConnected() {
		return nil, conf, errors.New("MQTT is not connected")
	}
	return c, conf, nil
}

func wait(t paho.Token) error {
	if t == nil {
		return nil
	}
	if !t.WaitTimeout(10 * time.Second) {
		return errors.New("broker did not acknowledge the publish")
	}
	return t.Error()
}

// RepublishDiscovery sends the discovery, state and sound messages again
// and returns the discovery topic.
func (b *Bridge) RepublishDiscovery() (string, error) {
	c, conf, err := b.connected()
	if err != nil {
		return "", err
	}
	if !conf.MQTT.Discovery {
		return "", errors.New("Home Assistant discovery is turned off")
	}
	if err := wait(c.Publish(conf.MQTT.BaseTopic+"/availability", 1, true, payloadOnline)); err != nil {
		return "", err
	}
	if err := wait(b.publishDiscovery(c, conf)); err != nil {
		return "", err
	}
	b.publishState(b.kiosk.State())
	b.PublishSounds()
	return DiscoveryTopic(conf), nil
}

// ClearRetained deletes this kiosk's retained messages from the broker:
// discovery (which removes the device from Home Assistant), state, sounds
// and availability. They come back on the next reconnect or republish.
func (b *Bridge) ClearRetained() ([]string, error) {
	c, conf, err := b.connected()
	if err != nil {
		return nil, err
	}
	base := conf.MQTT.BaseTopic
	topics := []string{DiscoveryTopic(conf), base + "/state", base + "/sounds", base + "/availability"}
	for _, t := range topics {
		if err := wait(c.Publish(t, 1, true, []byte{})); err != nil {
			return nil, fmt.Errorf("%s: %w", t, err)
		}
	}
	b.log.Info("cleared retained messages", "topics", topics)
	return topics, nil
}

func Discovery(cfg config.Config, version, webURL string) map[string]any {
	base := cfg.MQTT.BaseTopic
	id := cfg.DeviceID
	state := base + "/state"
	cmp := func(platform, key, name string, extra map[string]any) (string, map[string]any) {
		m := map[string]any{"p": platform, "name": name, "unique_id": id + "_" + key}
		for k, v := range extra {
			m[k] = v
		}
		return key, m
	}
	cmps := map[string]any{}
	add := func(k string, v map[string]any) { cmps[k] = v }

	if names := cfg.ViewNames(); len(names) > 0 {
		add(cmp("select", "view", "View", map[string]any{
			"command_topic": base + "/cmd/view", "state_topic": state,
			"value_template": "{{ value_json.view if value_json.view else None }}", "options": names, "icon": "mdi:monitor-dashboard",
		}))
	}
	add(cmp("text", "url", "URL", map[string]any{
		"command_topic": base + "/cmd/url", "state_topic": state,
		"value_template": "{{ value_json.url if value_json.mode == 'web' else '' }}", "max": 255, "icon": "mdi:web",
	}))
	add(cmp("text", "stream", "Stream", map[string]any{
		"command_topic": base + "/cmd/stream", "state_topic": state,
		"value_template": "{{ value_json.url if value_json.mode == 'stream' else '' }}", "max": 255, "icon": "mdi:cctv",
	}))
	add(cmp("switch", "screen", "Screen", map[string]any{
		"command_topic": base + "/cmd/screen", "state_topic": state,
		"value_template": "{{ 'ON' if value_json.screen else 'OFF' }}", "icon": "mdi:monitor",
	}))
	add(cmp("number", "brightness", "Brightness", map[string]any{
		"command_topic": base + "/cmd/brightness", "state_topic": state,
		"value_template": "{{ value_json.brightness if value_json.brightness >= 0 else None }}",
		"min":            0, "max": 100, "step": 1, "unit_of_measurement": "%", "mode": "slider", "icon": "mdi:brightness-6",
	}))
	if names := cfg.RotationNames(); len(names) > 0 {
		add(cmp("select", "rotation", "Rotation", map[string]any{
			"command_topic": base + "/cmd/rotation", "state_topic": state,
			"value_template": "{{ value_json.rotation if value_json.rotation_active else None }}",
			"options":        names, "icon": "mdi:rotate-right",
		}))
	}
	add(cmp("binary_sensor", "rotating", "Rotating", map[string]any{
		"state_topic": state, "value_template": "{{ 'ON' if value_json.rotation_active else 'OFF' }}",
		"icon": "mdi:rotate-right",
	}))
	add(cmp("binary_sensor", "casting", "Casting", map[string]any{
		"state_topic": state, "value_template": "{{ 'ON' if value_json.casting else 'OFF' }}",
		"icon": "mdi:cast",
	}))
	add(cmp("button", "resume_rotation", "Resume rotation", map[string]any{
		"command_topic": base + "/cmd/resume_rotation", "icon": "mdi:play",
	}))
	add(cmp("button", "stop_rotation", "Stop rotation", map[string]any{
		"command_topic": base + "/cmd/stop_rotation", "icon": "mdi:pause",
	}))
	add(cmp("button", "next", "Next view", map[string]any{
		"command_topic": base + "/cmd/next", "icon": "mdi:skip-next",
	}))
	add(cmp("text", "play_sound", "Play sound", map[string]any{
		"command_topic": base + "/cmd/play_sound", "state_topic": state,
		"value_template": "{{ value_json.sound }}", "max": 255, "icon": "mdi:volume-high",
	}))
	add(cmp("button", "stop_sound", "Stop sound", map[string]any{
		"command_topic": base + "/cmd/stop_sound", "icon": "mdi:volume-off",
	}))
	add(cmp("number", "volume", "Volume", map[string]any{
		"command_topic": base + "/cmd/volume", "state_topic": state,
		"value_template": "{{ value_json.volume if value_json.volume >= 0 else None }}",
		"min":            0, "max": 100, "step": 1, "unit_of_measurement": "%", "mode": "slider", "icon": "mdi:volume-medium",
	}))
	add(cmp("sensor", "cpu_temperature", "CPU temperature", map[string]any{
		"state_topic": state, "value_template": "{{ value_json.device.cpu_temp_c | default(None) }}",
		"device_class": "temperature", "unit_of_measurement": "°C", "state_class": "measurement",
		"entity_category": "diagnostic",
	}))
	add(cmp("sensor", "ip_address", "IP address", map[string]any{
		"state_topic": state, "value_template": "{{ value_json.device.ip | default(None) }}",
		"entity_category": "diagnostic", "icon": "mdi:ip-network",
	}))
	add(cmp("sensor", "uptime", "Uptime", map[string]any{
		"state_topic": state, "value_template": "{{ value_json.device.boot_time | default(None) }}",
		"device_class": "timestamp", "entity_category": "diagnostic",
	}))
	add(cmp("sensor", "browser_restarts", "Browser restarts", map[string]any{
		"state_topic": state, "value_template": "{{ value_json.device.browser_restarts | default(0) }}",
		"state_class": "total_increasing", "entity_category": "diagnostic", "icon": "mdi:restart-alert",
	}))
	add(cmp("sensor", "mode", "Mode", map[string]any{
		"state_topic": state, "value_template": "{{ value_json.mode }}", "icon": "mdi:television-play",
	}))
	add(cmp("sensor", "last_activity", "Last activity", map[string]any{
		"state_topic": state, "value_template": "{{ value_json.last_activity }}", "device_class": "timestamp",
		"entity_category": "diagnostic",
	}))
	add(cmp("button", "reload", "Reload", map[string]any{
		"command_topic": base + "/cmd/reload", "icon": "mdi:refresh",
	}))
	add(cmp("button", "stop_stream", "Stop stream", map[string]any{
		"command_topic": base + "/cmd/stop_stream", "icon": "mdi:stop",
	}))
	add(cmp("button", "restart_browser", "Restart browser", map[string]any{
		"command_topic": base + "/cmd/restart_browser", "entity_category": "config", "icon": "mdi:restart",
	}))
	add(cmp("button", "reboot", "Reboot", map[string]any{
		"command_topic": base + "/cmd/reboot", "device_class": "restart", "entity_category": "config",
	}))

	dev := map[string]any{
		"identifiers":  []string{id},
		"name":         cfg.DeviceName,
		"manufacturer": "pikiosk",
		"model":        "Kiosk",
		"sw_version":   version,
	}
	if webURL != "" {
		dev["configuration_url"] = webURL
	}
	return map[string]any{
		"device":             dev,
		"origin":             map[string]any{"name": "pikiosk", "sw_version": version, "support_url": "https://github.com/XtremeOwnage/pikiosk"},
		"availability_topic": base + "/availability",
		"components":         cmps,
		"qos":                1,
	}
}
