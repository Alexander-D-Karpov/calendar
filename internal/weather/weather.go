package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
)

const (
	DefaultURL = "https://api.open-meteo.com/v1/forecast"
	ttl        = 15 * time.Minute
	retryAfter = time.Minute
	maxPlaces  = 256
)

type Current struct {
	Temp float64
	Code int
	Day  bool
	At   time.Time
}

type entry struct {
	cur     *Current
	fetched time.Time
	tried   time.Time
}

type Service struct {
	client *http.Client
	url    string
	clock  clock.Clock
	mu     sync.Mutex
	cache  map[string]*entry
}

func New(client *http.Client, baseURL string, clk clock.Clock) *Service {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	if clk == nil {
		clk = clock.New()
	}
	return &Service{client: client, url: baseURL, clock: clk, cache: map[string]*entry{}}
}

func round(v float64) float64 {
	return math.Round(v*100) / 100
}

// Current answers from the cache and refetches at most once per ttl per place;
// a failed fetch keeps the stale reading and is not retried for a minute.
// Fetches are rare enough that one lock for every place is fine.
func (s *Service) Current(ctx context.Context, lat, lon float64) *Current {
	lat, lon = round(lat), round(lon)
	key := strconv.FormatFloat(lat, 'f', 2, 64) + "," + strconv.FormatFloat(lon, 'f', 2, 64)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	e := s.cache[key]
	if e != nil && (now.Sub(e.fetched) < ttl || now.Sub(e.tried) < retryAfter) {
		return e.cur
	}
	if e == nil {
		if len(s.cache) >= maxPlaces {
			clear(s.cache)
		}
		e = &entry{}
		s.cache[key] = e
	}
	e.tried = now
	if cur, err := s.get(ctx, lat, lon); err == nil {
		e.cur, e.fetched = cur, now
	}
	return e.cur
}

func (s *Service) get(ctx context.Context, lat, lon float64) (*Current, error) {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(lat, 'f', 2, 64))
	q.Set("longitude", strconv.FormatFloat(lon, 'f', 2, 64))
	q.Set("current", "temperature_2m,weather_code,is_day")
	q.Set("timeformat", "unixtime")
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather: open-meteo answered %s", resp.Status)
	}
	var body struct {
		Current struct {
			Time  int64   `json:"time"`
			Temp  float64 `json:"temperature_2m"`
			Code  int     `json:"weather_code"`
			IsDay int     `json:"is_day"`
		} `json:"current"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(&body); err != nil {
		return nil, fmt.Errorf("weather: %w", err)
	}
	c := body.Current
	return &Current{Temp: c.Temp, Code: c.Code, Day: c.IsDay == 1, At: time.Unix(c.Time, 0).UTC()}, nil
}
