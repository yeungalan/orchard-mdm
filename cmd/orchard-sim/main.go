// Command orchard-sim enrolls simulated iOS devices against an Orchard MDM
// server for demos, load tests and UI development. It needs an enrollment
// link (or token) and a server whose APNs certificate is configured; since
// simulated devices cannot receive pushes, they poll instead.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/devicesim"
)

func main() {
	enrollURL := flag.String("enroll", "", "enrollment link, e.g. https://mdm.example.com/enroll/<token>")
	count := flag.Int("n", 5, "number of devices to simulate")
	interval := flag.Duration("poll", 30*time.Second, "how often each device polls for commands")
	insecure := flag.Bool("insecure", false, "skip TLS verification (self-signed test servers)")
	once := flag.Bool("once", false, "enroll, process queued commands once, and exit")
	flag.Parse()
	if *enrollURL == "" {
		log.Fatal("-enroll is required")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if *insecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	profileURL := strings.TrimRight(*enrollURL, "/")
	if !strings.HasSuffix(profileURL, "/profile") {
		profileURL += "/profile"
	}
	products := []string{"iPhone17,1", "iPhone16,2", "iPhone15,3", "iPad14,1", "iPad16,3", "iPhone17,3"}
	names := []string{"Front desk", "Warehouse", "Sales", "Field tech", "Kiosk", "Reception", "Driver", "Clinic", "Lab", "Store"}
	var wg sync.WaitGroup
	for i := 0; i < *count; i++ {
		resp, err := client.Get(profileURL)
		if err != nil {
			log.Fatal(err)
		}
		prof, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Fatalf("profile download: %s %s", resp.Status, prof)
		}
		product := products[i%len(products)]
		kind := "iPhone"
		if strings.HasPrefix(product, "iPad") {
			kind = "iPad"
		}
		dev := devicesim.New(fmt.Sprintf("%s %s %d", names[i%len(names)], kind, i+1), product)
		dev.HTTP = client
		dev.OSVersion = []string{"18.6", "18.6", "18.5", "17.7", "26.0"}[i%5]
		if err := dev.Enroll(prof); err != nil {
			log.Fatalf("enroll %d: %v", i, err)
		}
		log.Printf("enrolled %s (%s, serial %s)", dev.Name, dev.UDID, dev.Serial)
		wg.Add(1)
		go func(d *devicesim.Device) {
			defer wg.Done()
			for {
				handled, err := d.Poll()
				if err != nil {
					log.Printf("%s: %v", d.Name, err)
				} else if len(handled) > 0 {
					log.Printf("%s handled %v", d.Name, handled)
				}
				if *once || !d.Enrolled() {
					return
				}
				time.Sleep(*interval + time.Duration(rand.Intn(5000))*time.Millisecond)
			}
		}(dev)
	}
	wg.Wait()
}
