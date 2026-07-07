package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
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
	ctx := context.Background()

	// 1. Get the current public IP
	currentIP, err := getPublicIP()
	if err != nil {
		log.Fatalf("Error fetching public IP: %v", err)
	}
	log.Printf("Current Public IP: %s", currentIP)

	// 2. Read Cloudflare API token from environment and initialize client
	apiToken := os.Getenv("CLOUDFLARE_API_TOKEN")
	if strings.TrimSpace(apiToken) == "" {
		log.Fatalf("Environment variable CLOUDFLARE_API_TOKEN is not set or empty")
	}

	api, err := cloudflare.NewWithAPIToken(apiToken)
	if err != nil {
		log.Fatalf("Error initializing Cloudflare client: %v", err)
	}

	// 3. Read zone name from environment and fetch the Zone ID
	zoneName := os.Getenv("CLOUDFLARE_ZONE")
	if strings.TrimSpace(zoneName) == "" {
		log.Fatalf("Environment variable CLOUDFLARE_ZONE is not set or empty")
	}

	zoneID, err := api.ZoneIDByName(zoneName)
	if err != nil {
		log.Fatalf("Error fetching Zone ID: %v", err)
	}

	// 4. Fetch existing DNS records for the zone
	// We look for 'A' records, change Type to 'CNAME' if you are mapping to a domain string instead
	records, _, err := api.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{
		Type: "A", 
	})
	if err != nil {
		log.Fatalf("Error fetching DNS records: %v", err)
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

	// 5. Compare and update records
	for _, record := range records {
		if targets[record.Name] {
			if record.Content == currentIP {
				log.Printf("[%s] IP matches (%s). No update needed.", record.Name, record.Content)
			} else {
				log.Printf("[%s] IP mismatch! DNS has %s, current is %s. Updating...", record.Name, record.Content, currentIP)
				
				// Update the record with the new IP
				_, err := api.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateDNSRecordParams{
					ID:      record.ID,
					Type:    "A", // Change to "CNAME" if using canonical names
					Name:    record.Name,
					Content: currentIP,
					TTL:     1, // 1 = Automatic TTL
					Proxied: record.Proxied, // Preserve existing proxy status (orange/grey cloud)
				})

				if err != nil {
					log.Printf("Failed to update [%s]: %v", record.Name, err)
				} else {
					log.Printf("[%s] Successfully updated to %s", record.Name, currentIP)
				}
			}
		}
	}
}