// Command orchard-sim enrolls simulated iOS devices against an Orchard MDM
// server for demos, load tests and UI development. It needs an enrollment
// link (or, with -account, a server with work account sign-in enabled) and a
// server whose APNs certificate is configured; since simulated devices cannot
// receive pushes, they poll instead.
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
	server := flag.String("server", "", "server base URL for -account enrollments, e.g. https://mdm.example.com")
	account := flag.String("account", "", "enroll by work account sign-in (User Enrollment / BYOD) as this account; with -n > 1 a number is added")
	code := flag.String("code", "", "enrollment code used to sign in with -account")
	flag.Parse()
	if *enrollURL == "" && *account == "" {
		log.Fatal("-enroll or -account is required")
	}
	if *account != "" && (*server == "" || *code == "") {
		log.Fatal("-account needs -server and -code")
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
	poll := func(d *devicesim.Device) {
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
	}
	for i := 0; i < *count && *account != ""; i++ {
		user, domain, _ := strings.Cut(*account, "@")
		id := *account
		if *count > 1 {
			id = fmt.Sprintf("%s%d@%s", user, i+1, domain)
		}
		dev := devicesim.New(fmt.Sprintf("%s's %s", strings.ToUpper(user[:1])+user[1:], []string{"iPhone", "iPad"}[i%2]), []string{"iPhone16,2", "iPad14,1"}[i%2])
		dev.HTTP = client
		if err := dev.AccountEnroll(*server, id, devicesim.CodeAuth(*code)); err != nil {
			log.Fatalf("account enroll %s: %v", id, err)
		}
		log.Printf("enrolled %s with User Enrollment (enrollment ID %s)", id, dev.UDID)
		wg.Add(1)
		go poll(dev)
	}
	for i := 0; i < *count && *account == ""; i++ {
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
		go poll(dev)
	}
	wg.Wait()
}
