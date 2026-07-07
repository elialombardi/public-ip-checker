package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudflare/cloudflare-go"
)

// Zone name will be read from the environment at runtime.

// List of subdomains you want to check and update
var domainsToUpdate = []string{
	"sub1.example.com",
	"sub2.example.com",
}

// getPublicIP fetches the current machine's public IPv4 address
func getPublicIP() (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(ip)), nil
}

func main() {
	// Use a context that cancels on SIGINT/SIGTERM so we can exit cleanly
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Read Cloudflare API token from environment and initialize client
	apiToken := os.Getenv("CLOUDFLARE_API_TOKEN")
	if strings.TrimSpace(apiToken) == "" {
		log.Fatalf("Environment variable CLOUDFLARE_API_TOKEN is not set or empty")
	}

	api, err := cloudflare.NewWithAPIToken(apiToken)
	if err != nil {
		log.Fatalf("Error initializing Cloudflare client: %v", err)
	}

	// 2. Read zone name from environment and fetch the Zone ID
	zoneName := os.Getenv("CLOUDFLARE_ZONE")
	if strings.TrimSpace(zoneName) == "" {
		log.Fatalf("Environment variable CLOUDFLARE_ZONE is not set or empty")
	}

	zoneID, err := api.ZoneIDByName(zoneName)
	if err != nil {
		log.Fatalf("Error fetching Zone ID: %v", err)
	}

	// If CLOUDFLARE_DOMAINS is set, use it (comma-separated list). Otherwise use the hardcoded list.
	if domainsEnv := os.Getenv("CLOUDFLARE_DOMAINS"); strings.TrimSpace(domainsEnv) != "" {
		parts := strings.Split(domainsEnv, ",")
		parsed := make([]string, 0, len(parts))
		for _, p := range parts {
			s := strings.TrimSpace(p)
			if s != "" {
				parsed = append(parsed, s)
			}
		}
		if len(parsed) == 0 {
			log.Fatalf("Environment variable CLOUDFLARE_DOMAINS is set but contains no valid domains")
		}
		domainsToUpdate = parsed
		log.Printf("Using domains from CLOUDFLARE_DOMAINS: %v", domainsToUpdate)
	} else {
		log.Printf("Using hardcoded domains: %v", domainsToUpdate)
	}

	// Create a map of our target domains for quick lookup
	targets := make(map[string]bool)
	for _, d := range domainsToUpdate {
		targets[d] = true
	}

	// Determine run interval from environment. Prefer RUN_INTERVAL (Go duration string),
	// fall back to RUN_INTERVAL_SECONDS (integer seconds). Default to 5m.
	var interval time.Duration
	if s := strings.TrimSpace(os.Getenv("RUN_INTERVAL")); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			log.Fatalf("Invalid RUN_INTERVAL value: %v", err)
		}
		interval = d
	} else if s := strings.TrimSpace(os.Getenv("RUN_INTERVAL_SECONDS")); s != "" {
		secs, err := strconv.Atoi(s)
		if err != nil || secs <= 0 {
			log.Fatalf("Invalid RUN_INTERVAL_SECONDS value: %v", err)
		}
		interval = time.Duration(secs) * time.Second
	} else {
		interval = 5 * time.Minute
	}

	log.Printf("Running update job every %s", interval)

	// Run once immediately, then on interval until cancelled
	if err := doUpdate(ctx, api, zoneID, targets); err != nil {
		log.Printf("Initial update failed: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutdown signal received, exiting")
			return
		case <-ticker.C:
			if err := doUpdate(ctx, api, zoneID, targets); err != nil {
				log.Printf("Update failed: %v", err)
			}
		}
	}
}

// doUpdate performs a single fetch-and-update cycle.
func doUpdate(ctx context.Context, api *cloudflare.API, zoneID string, targets map[string]bool) error {
	// Get the current public IP
	currentIP, err := getPublicIP()
	if err != nil {
		return err
	}
	log.Printf("Current Public IP: %s", currentIP)

	// Fetch existing DNS A records for the zone
	records, _, err := api.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{Type: "A"})
	if err != nil {
		return err
	}

	// Compare and update records as needed
	for _, record := range records {
		if targets[record.Name] {
			if record.Content == currentIP {
				log.Printf("[%s] IP matches (%s). No update needed.", record.Name, record.Content)
			} else {
				log.Printf("[%s] IP mismatch! DNS has %s, current is %s. Updating...", record.Name, record.Content, currentIP)
				_, err := api.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateDNSRecordParams{
					ID:      record.ID,
					Type:    "A",
					Name:    record.Name,
					Content: currentIP,
					TTL:     1,
					Proxied: record.Proxied,
				})

				if err != nil {
					log.Printf("Failed to update [%s]: %v", record.Name, err)
				} else {
					log.Printf("[%s] Successfully updated to %s", record.Name, currentIP)
				}
			}
		}
	}

	return nil
}