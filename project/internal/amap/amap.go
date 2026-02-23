package amap

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client wraps calls to AMap (高德) Web Service API
type Client struct {
	APIKey  string
	Timeout time.Duration
}

// NewClient creates an AMap client from environment variable AMAP_KEY
func NewClient() *Client {
	return &Client{
		APIKey:  os.Getenv("AMAP_KEY"),
		Timeout: 10 * time.Second,
	}
}

// Available returns true if the AMap API key is configured
func (c *Client) Available() bool {
	return c.APIKey != ""
}

// ======================== Reverse Geocoding ========================

// ReGeoResult holds the reverse geocoding result
type ReGeoResult struct {
	Address   string `json:"address"`
	Province  string `json:"province"`
	City      string `json:"city"`
	District  string `json:"district"`
	Township  string `json:"township"`
	Formatted string `json:"formatted"`
}

type reGeoResponse struct {
	Status    string `json:"status"`
	Info      string `json:"info"`
	ReGeoCode struct {
		FormattedAddress string `json:"formatted_address"`
		AddressComponent struct {
			Province string `json:"province"`
			City     interface{} `json:"city"`
			District string `json:"district"`
			Township string `json:"township"`
		} `json:"addressComponent"`
	} `json:"regeocode"`
}

// ReverseGeocode converts lat/lon to a human-readable address
func (c *Client) ReverseGeocode(lat, lon float64) (*ReGeoResult, error) {
	if !c.Available() {
		return nil, fmt.Errorf("AMAP_KEY not configured")
	}

	// AMap uses lon,lat order (not lat,lon)
	location := fmt.Sprintf("%.6f,%.6f", lon, lat)
	u := fmt.Sprintf("https://restapi.amap.com/v3/geocode/regeo?key=%s&location=%s&extensions=base",
		url.QueryEscape(c.APIKey), url.QueryEscape(location))

	client := &http.Client{Timeout: c.Timeout}
	resp, err := client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("amap request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result reGeoResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.Status != "1" {
		return nil, fmt.Errorf("amap error: %s", result.Info)
	}

	city := ""
	switch v := result.ReGeoCode.AddressComponent.City.(type) {
	case string:
		city = v
	case []interface{}:
		// AMap sometimes returns empty array for city
		if len(v) > 0 {
			city = fmt.Sprintf("%v", v[0])
		}
	}

	return &ReGeoResult{
		Address:   result.ReGeoCode.FormattedAddress,
		Province:  result.ReGeoCode.AddressComponent.Province,
		City:      city,
		District:  result.ReGeoCode.AddressComponent.District,
		Township:  result.ReGeoCode.AddressComponent.Township,
		Formatted: result.ReGeoCode.FormattedAddress,
	}, nil
}

// ======================== Walking/Driving Direction ========================

// DirectionResult holds a simplified route result
type DirectionResult struct {
	Distance string      `json:"distance"` // meters
	Duration string      `json:"duration"` // seconds
	Polyline [][]float64 `json:"polyline"` // [[lon,lat], ...]
}

type directionResponse struct {
	Status string `json:"status"`
	Info   string `json:"info"`
	Route  struct {
		Paths []struct {
			Distance string `json:"distance"`
			Duration string `json:"duration"`
			Steps    []struct {
				Polyline string `json:"polyline"`
			} `json:"steps"`
		} `json:"paths"`
	} `json:"route"`
}

// GetWalkingRoute gets a walking route between two points (as reference corridor for drone)
func (c *Client) GetWalkingRoute(startLat, startLon, endLat, endLon float64) (*DirectionResult, error) {
	if !c.Available() {
		return nil, fmt.Errorf("AMAP_KEY not configured")
	}

	origin := fmt.Sprintf("%.6f,%.6f", startLon, startLat)
	dest := fmt.Sprintf("%.6f,%.6f", endLon, endLat)
	u := fmt.Sprintf("https://restapi.amap.com/v3/direction/walking?key=%s&origin=%s&destination=%s",
		url.QueryEscape(c.APIKey), url.QueryEscape(origin), url.QueryEscape(dest))

	client := &http.Client{Timeout: c.Timeout}
	resp, err := client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("amap direction request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result directionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.Status != "1" {
		return nil, fmt.Errorf("amap direction error: %s", result.Info)
	}
	if len(result.Route.Paths) == 0 {
		return nil, fmt.Errorf("no route found")
	}

	path := result.Route.Paths[0]

	// Parse polyline from all steps
	var polyline [][]float64
	for _, step := range path.Steps {
		points := strings.Split(step.Polyline, ";")
		for _, pt := range points {
			coords := strings.Split(pt, ",")
			if len(coords) == 2 {
				var lon, lat float64
				fmt.Sscanf(coords[0], "%f", &lon)
				fmt.Sscanf(coords[1], "%f", &lat)
				polyline = append(polyline, []float64{lon, lat})
			}
		}
	}

	return &DirectionResult{
		Distance: path.Distance,
		Duration: path.Duration,
		Polyline: polyline,
	}, nil
}

// ======================== POI Search ========================

// POIItem represents a point of interest
type POIItem struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Address  string  `json:"address"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Distance string  `json:"distance"`
}

type poiResponse struct {
	Status string `json:"status"`
	Info   string `json:"info"`
	Pois   []struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Address  string `json:"address"`
		Location string `json:"location"`
		Distance string `json:"distance"`
	} `json:"pois"`
}

// SearchNearbyPOI searches for sensitive POIs (airports, schools, hospitals) near a point
func (c *Client) SearchNearbyPOI(lat, lon float64, radiusM int) ([]POIItem, error) {
	if !c.Available() {
		return nil, fmt.Errorf("AMAP_KEY not configured")
	}

	location := fmt.Sprintf("%.6f,%.6f", lon, lat)
	// Types: 机场(150104) + 学校(141200) + 医院(090100)
	types := "150104|141200|090100"
	u := fmt.Sprintf("https://restapi.amap.com/v3/place/around?key=%s&location=%s&radius=%d&types=%s&offset=10",
		url.QueryEscape(c.APIKey), url.QueryEscape(location), radiusM, url.QueryEscape(types))

	client := &http.Client{Timeout: c.Timeout}
	resp, err := client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("amap poi request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result poiResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.Status != "1" {
		return nil, fmt.Errorf("amap poi error: %s", result.Info)
	}

	var items []POIItem
	for _, p := range result.Pois {
		var pLon, pLat float64
		coords := strings.Split(p.Location, ",")
		if len(coords) == 2 {
			fmt.Sscanf(coords[0], "%f", &pLon)
			fmt.Sscanf(coords[1], "%f", &pLat)
		}
		items = append(items, POIItem{
			Name:     p.Name,
			Type:     p.Type,
			Address:  p.Address,
			Lat:      pLat,
			Lon:      pLon,
			Distance: p.Distance,
		})
	}
	return items, nil
}

// ======================== Forward Geocoding ========================

// GeoCandidate represents one geocoding result candidate
type GeoCandidate struct {
	Name      string  `json:"name"`
	Formatted string  `json:"formatted_address"`
	Province  string  `json:"province"`
	City      string  `json:"city"`
	District  string  `json:"district"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Level     string  `json:"level"`
}

type geoResponse struct {
	Status   string `json:"status"`
	Info     string `json:"info"`
	Geocodes []struct {
		FormattedAddress string `json:"formatted_address"`
		Province         string `json:"province"`
		City             interface{} `json:"city"`
		District         string `json:"district"`
		Location         string `json:"location"`
		Level            string `json:"level"`
	} `json:"geocodes"`
}

// Geocode converts an address string to a list of coordinate candidates
func (c *Client) Geocode(address string, city string) ([]GeoCandidate, error) {
	if !c.Available() {
		return nil, fmt.Errorf("AMAP_KEY not configured")
	}
	if address == "" {
		return nil, fmt.Errorf("address is empty")
	}

	u := fmt.Sprintf("https://restapi.amap.com/v3/geocode/geo?key=%s&address=%s",
		url.QueryEscape(c.APIKey), url.QueryEscape(address))
	if city != "" {
		u += "&city=" + url.QueryEscape(city)
	}

	client := &http.Client{Timeout: c.Timeout}
	resp, err := client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("amap geocode request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result geoResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.Status != "1" {
		return nil, fmt.Errorf("amap geocode error: %s", result.Info)
	}

	var candidates []GeoCandidate
	for _, g := range result.Geocodes {
		var lon, lat float64
		coords := strings.Split(g.Location, ",")
		if len(coords) == 2 {
			fmt.Sscanf(coords[0], "%f", &lon)
			fmt.Sscanf(coords[1], "%f", &lat)
		}
		cityStr := ""
		switch v := g.City.(type) {
		case string:
			cityStr = v
		case []interface{}:
			if len(v) > 0 {
				cityStr = fmt.Sprintf("%v", v[0])
			}
		}
		candidates = append(candidates, GeoCandidate{
			Name:      address,
			Formatted: g.FormattedAddress,
			Province:  g.Province,
			City:      cityStr,
			District:  g.District,
			Lat:       lat,
			Lon:       lon,
			Level:     g.Level,
		})
	}
	return candidates, nil
}

// ======================== Build Map Context for LLM ========================

// MapContext holds all map-derived info to feed into LLM prompt
type MapContext struct {
	StartAddress string        `json:"start_address"`
	GoalAddress  string        `json:"goal_address"`
	RefPolyline  [][]float64   `json:"ref_polyline,omitempty"`
	RefDistance   string        `json:"ref_distance,omitempty"`
	NearbyPOIs   []POIItem     `json:"nearby_pois,omitempty"`
	Summary      string        `json:"summary"`
}

// BuildContext gathers map data for a start→goal pair and returns a structured context
func (c *Client) BuildContext(startLat, startLon, goalLat, goalLon float64) *MapContext {
	ctx := &MapContext{}
	var summaryParts []string

	// Reverse geocode start
	if startGeo, err := c.ReverseGeocode(startLat, startLon); err == nil {
		ctx.StartAddress = startGeo.Formatted
		summaryParts = append(summaryParts, fmt.Sprintf("起点位于: %s", startGeo.Formatted))
	}

	// Reverse geocode goal
	if goalGeo, err := c.ReverseGeocode(goalLat, goalLon); err == nil {
		ctx.GoalAddress = goalGeo.Formatted
		summaryParts = append(summaryParts, fmt.Sprintf("终点位于: %s", goalGeo.Formatted))
	}

	// Get reference walking route
	if route, err := c.GetWalkingRoute(startLat, startLon, goalLat, goalLon); err == nil {
		ctx.RefPolyline = route.Polyline
		ctx.RefDistance = route.Distance
		summaryParts = append(summaryParts, fmt.Sprintf("地面参考路线距离: %sm", route.Distance))
	}

	// Search nearby sensitive POIs (3km radius from midpoint)
	midLat := (startLat + goalLat) / 2
	midLon := (startLon + goalLon) / 2
	if pois, err := c.SearchNearbyPOI(midLat, midLon, 3000); err == nil && len(pois) > 0 {
		ctx.NearbyPOIs = pois
		var poiNames []string
		for _, p := range pois {
			poiNames = append(poiNames, fmt.Sprintf("%s(%s,距%.0sm)", p.Name, p.Type, p.Distance))
		}
		summaryParts = append(summaryParts, fmt.Sprintf("附近敏感区域: %s", strings.Join(poiNames, "; ")))
	}

	ctx.Summary = strings.Join(summaryParts, "\n")
	return ctx
}
