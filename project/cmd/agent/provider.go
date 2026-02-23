package main

// DroneDataProvider abstracts the source of GPS, battery, and flight phase data.
// Implementations include:
//   - SimulatedProvider: generates fake data for demo/testing
//   - MAVLinkProvider:   reads real data from a flight controller via MAVLink protocol
//   - NMEAProvider:      reads GPS from a serial NMEA device (battery/flight from defaults)
type DroneDataProvider interface {
	// Name returns a human-readable name for this provider (e.g. "MAVLink", "Simulated")
	Name() string

	// Start initializes the provider (open connections, start background readers).
	// It should be non-blocking; background goroutines are fine.
	Start() error

	// Stop cleanly shuts down the provider.
	Stop()

	// Tick is called once per push cycle to advance internal state (e.g. simulation step).
	Tick()

	// GPSPayload returns the GPS data to push, keyed for the JSON protocol.
	GPSPayload(agentID string) map[string]interface{}

	// BatteryPayload returns the battery data to push.
	BatteryPayload(agentID string) map[string]interface{}

	// FlightPhase returns the current flight phase string, or "" if no active phase.
	FlightPhase() string

	// FlightPayload returns the flight mission phase data to push.
	FlightPayload(agentID string) map[string]interface{}

	// IsReady returns true if the provider has received at least one valid data point.
	IsReady() bool
}
