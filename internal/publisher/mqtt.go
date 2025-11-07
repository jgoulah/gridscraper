package publisher

import (
	"fmt"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/jgoulah/gridscraper/internal/config"
	"github.com/jgoulah/gridscraper/pkg/models"
)

// Publisher handles MQTT publishing to Home Assistant
type Publisher struct {
	client      mqtt.Client
	topicPrefix string
}

// New creates a new MQTT publisher
func New(cfg config.MQTTConfig) (*Publisher, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("MQTT is not enabled in config")
	}

	if cfg.Broker == "" {
		return nil, fmt.Errorf("MQTT broker address is required")
	}

	// Set default topic prefix if not specified
	topicPrefix := cfg.TopicPrefix
	if topicPrefix == "" {
		topicPrefix = "electric_meter"
	}

	// Configure MQTT client options
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s", cfg.Broker))
	opts.SetClientID("gridscraper")
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectTimeout(10 * time.Second)

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
	}
	if cfg.Password != "" {
		opts.SetPassword(cfg.Password)
	}

	// Create and connect client
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("connecting to MQTT broker: %w", token.Error())
	}

	return &Publisher{
		client:      client,
		topicPrefix: topicPrefix,
	}, nil
}

// Publish sends a usage reading to MQTT
func (p *Publisher) Publish(reading models.UsageData) error {
	if !p.client.IsConnected() {
		return fmt.Errorf("MQTT client is not connected")
	}

	// Publish value
	valueTopic := fmt.Sprintf("%s/value", p.topicPrefix)
	if token := p.client.Publish(valueTopic, 0, false, fmt.Sprintf("%.2f", reading.KWh)); token.Wait() && token.Error() != nil {
		return fmt.Errorf("publishing value: %w", token.Error())
	}

	// Publish unit of measure
	uomTopic := fmt.Sprintf("%s/uom", p.topicPrefix)
	if token := p.client.Publish(uomTopic, 0, false, "kWh"); token.Wait() && token.Error() != nil {
		return fmt.Errorf("publishing uom: %w", token.Error())
	}

	// Publish start time (the date of the reading in ISO8601 format)
	startTimeTopic := fmt.Sprintf("%s/startTime", p.topicPrefix)
	startTime := reading.Date.Format(time.RFC3339)
	if token := p.client.Publish(startTimeTopic, 0, false, startTime); token.Wait() && token.Error() != nil {
		return fmt.Errorf("publishing startTime: %w", token.Error())
	}

	return nil
}

// Close disconnects from the MQTT broker
func (p *Publisher) Close() {
	if p.client != nil && p.client.IsConnected() {
		p.client.Disconnect(250)
	}
}
