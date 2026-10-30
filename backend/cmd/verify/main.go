package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type check struct {
	id    string
	title string
	run   func(*suite) error
}

type outcome struct {
	check
	err     error
	elapsed time.Duration
}

func main() {
	s := newSuite()
	started := time.Now()
	if err := s.login(); err != nil {
		fmt.Printf("Giriş başarısız, doğrulama durduruldu: %v\n", err)
		os.Exit(1)
	}
	results := make([]outcome, 0, len(checks))
	for _, c := range checks {
		fmt.Printf("… %-8s %s\n", c.id, c.title)
		begin := time.Now()
		results = append(results, outcome{c, safely(s, c), time.Since(begin)})
	}
	failed := 0
	fmt.Printf("\n%-10s %-6s %8s  %s\n", "KİMLİK", "SONUÇ", "SÜRE", "AÇIKLAMA")
	fmt.Println(strings.Repeat("-", 92))
	for _, r := range results {
		status := "PASS"
		if r.err != nil {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%-10s %-6s %7.2fs  %s\n", r.id, status, r.elapsed.Seconds(), r.title)
		if r.err != nil {
			fmt.Printf("           └─ %v\n", r.err)
		}
	}
	fmt.Println(strings.Repeat("-", 92))
	fmt.Printf("Toplam %d kontrol, %d başarılı, %d başarısız (%.1f sn)\n", len(results), len(results)-failed, failed, time.Since(started).Seconds())
	if failed > 0 {
		os.Exit(1)
	}
}

func safely(s *suite, c check) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("panik: %v", value)
		}
	}()
	return c.run(s)
}
