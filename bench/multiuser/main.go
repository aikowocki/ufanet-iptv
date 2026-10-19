// Command multiuser создаёт пользователей через admin API и нагружает GET /api/v1/channels
// запросами от случайных пользователей. Показывает посекундную динамику, поэтому годится
// и для холодного старта: кэши пустые, каждый новый пользователь первый раз платит за argon2id.
//
//	go run ./bench/multiuser -setup -users 200          # один раз на свежей базе
//	go run ./bench/multiuser -users 200 -c 50 -d 20s    # нагрузка
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	base := flag.String("url", "http://127.0.0.1:18080", "адрес API")
	users := flag.Int("users", 200, "число пользователей")
	conc := flag.Int("c", 50, "параллелизм")
	dur := flag.Duration("d", 20*time.Second, "длительность нагрузки")
	setup := flag.Bool("setup", false, "только создать пользователей и выйти")
	admin := flag.String("admin", "admin:admin123", "учётная запись администратора login:password")
	showSecs := flag.Int("timeline", 10, "сколько первых секунд показать построчно")
	flag.Parse()

	if err := run(*base, *users, *conc, *dur, *setup, *admin, *showSecs); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func login(i int) (string, string) { return fmt.Sprintf("load%04d", i), fmt.Sprintf("pass-%04d-x", i) }

func run(base string, users, conc int, dur time.Duration, setup bool, admin string, showSecs int) error {
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{MaxIdleConnsPerHost: conc, MaxConnsPerHost: conc},
	}
	if err := waitHealthy(client, base); err != nil {
		return err
	}
	if setup {
		return createUsers(client, base, users, admin)
	}
	return load(client, base, users, conc, dur, showSecs)
}

func waitHealthy(c *http.Client, base string) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/healthz", nil)
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("%s не отвечает", base)
}

func createUsers(c *http.Client, base string, n int, admin string) error {
	a, p, _ := strings.Cut(admin, ":")
	post := func(path string, body any, out any) (int, error) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+path, bytes.NewReader(b))
		req.SetBasicAuth(a, p)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		if out != nil && resp.StatusCode < 300 {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode, nil
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // argon2id дорог, много параллельных создаёт 503
	var mu sync.Mutex
	created, existed, failed := 0, 0, 0
	for i := 1; i <= n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			l, pw := login(i)
			var u struct {
				ID int64 `json:"id"`
			}
			code, err := post("/api/v1/admin/users", map[string]any{"login": l, "password": pw}, &u)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil || (code != http.StatusCreated && code != http.StatusConflict):
				failed++
			case code == http.StatusConflict:
				existed++
			default:
				created++
				// Пакет 1 из seed: список каналов с подпиской, как у настоящего абонента.
				_, _ = post(fmt.Sprintf("/api/v1/admin/users/%d/subscriptions", u.ID), map[string]any{"package_id": 1}, nil)
			}
		}()
	}
	wg.Wait()
	fmt.Printf("пользователи: создано %d, уже были %d, ошибок %d\n", created, existed, failed)
	if failed > 0 {
		return fmt.Errorf("не удалось создать %d пользователей", failed)
	}
	return nil
}

type sample struct {
	at   time.Duration
	lat  time.Duration
	code int
}

func load(c *http.Client, base string, users, conc int, dur time.Duration, showSecs int) error {
	var mu sync.Mutex
	var samples []sample
	start := time.Now()
	deadline := start.Add(dur)
	var wg sync.WaitGroup
	for range conc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]sample, 0, 4096)
			for time.Now().Before(deadline) {
				l, pw := login(rand.IntN(users) + 1) //nolint:gosec // выбор пользователя для нагрузки, не криптография
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/api/v1/channels", nil)
				req.SetBasicAuth(l, pw)
				t0 := time.Now()
				code := 0
				if resp, err := c.Do(req); err == nil {
					_, _ = bytes.NewBuffer(nil).ReadFrom(resp.Body)
					_ = resp.Body.Close()
					code = resp.StatusCode
				}
				local = append(local, sample{at: t0.Sub(start), lat: time.Since(t0), code: code})
			}
			mu.Lock()
			samples = append(samples, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	report(samples, dur, users, conc, showSecs)
	return nil
}

func report(samples []sample, dur time.Duration, users, conc, showSecs int) {
	fmt.Printf("нагрузка: %d пользователей, c=%d, %s, запросов %d\n\n", users, conc, dur, len(samples))
	fmt.Println("сек   запросов   200    503   другие   p99, мс")
	secs := int(dur.Seconds())
	for s := 0; s < min(showSecs, secs); s++ {
		var n, ok, busy, other int
		var lats []time.Duration
		for _, x := range samples {
			if int(x.at.Seconds()) != s {
				continue
			}
			n++
			lats = append(lats, x.lat)
			switch x.code {
			case 200:
				ok++
			case 503:
				busy++
			default:
				other++
			}
		}
		fmt.Printf("%3d %10d %6d %6d %8d %9.1f\n", s+1, n, ok, busy, other, ms(percentile(lats, 0.99)))
	}
	codes := map[int]int{}
	lats := make([]time.Duration, 0, len(samples))
	var sum time.Duration
	for _, x := range samples {
		codes[x.code]++
		lats = append(lats, x.lat)
		sum += x.lat
	}
	fmt.Printf("\nвсего: %.0f RPS, средняя %.1f мс, p50 %.1f мс, p99 %.1f мс, макс %.1f мс\n",
		float64(len(samples))/dur.Seconds(), ms(sum/time.Duration(max(len(samples), 1))),
		ms(percentile(lats, 0.5)), ms(percentile(lats, 0.99)), ms(percentile(lats, 1)))
	fmt.Print("коды ответов:")
	for code, n := range codes {
		fmt.Printf(" [%d]=%d", code, n)
	}
	fmt.Println()
}

func percentile(l []time.Duration, q float64) time.Duration {
	if len(l) == 0 {
		return 0
	}
	sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
	return l[min(int(float64(len(l)-1)*q), len(l)-1)]
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
