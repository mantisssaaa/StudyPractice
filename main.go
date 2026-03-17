package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type PingResult struct {
	Host      string
	Success   bool
	Latency   time.Duration
	Timestamp time.Time
}

func main() {

	count := flag.Int("c", 1, "количество повторов для разовой проверки")
	isMonitor := flag.Bool("monitor", false, "режим постоянного мониторинга")
	useIcmp := flag.Bool("icmp", false, "использовать настоящий ICMP ping")
	flag.Parse()

	hostsFile := flag.Arg(0)
	if hostsFile == "" {
		fmt.Println("Использование: go run main.go [-monitor] [-c количество] [-icmp] <файл_хостов>")
		return
	}

	hosts, err := readHosts(hostsFile)
	if err != nil {
		log.Fatalf("Ошибка чтения файла: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logFile, err := os.OpenFile("monitor.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0644)
	if err != nil {
		log.Fatalf("Ошибка лога: %v", err)
	}
	defer logFile.Close()

	if *isMonitor {
		fmt.Printf("РЕЖИМ МОНИТОРИНГА: %d хостов (интервал 5 сек)\n", len(hosts))
		for _, host := range hosts {
			wg.Add(1)
			go func(h string) {
				defer wg.Done()
				for {
					success, latency, _ := pingHost(h, *useIcmp)
					ts := time.Now()
					status := "Timeout"
					if success {
						status = "OK"
					}

					out := fmt.Sprintf("%s | %s | %s | %v\n", 
						ts.Format("2006-01-02 15:04:05"), h, status, latency.Round(time.Millisecond))
					
					fmt.Print(out)
					logFile.WriteString(out)

					select {
					case <-ctx.Done():
						return
					case <-time.After(5 * time.Second):
					}
				}
			}(host)
		}
	} else {
		fmt.Println("РЕЖИМ РАЗОВОЙ ПРОВЕРКИ")
		fmt.Printf("%-20s | %-10s | %-12s | %-8s\n", "Хост", "Статус", "Время", "Попытка")
		for _, host := range hosts {
			for i := 1; i <= *count; i++ {
				success, latency, _ := pingHost(host, *useIcmp)
				status := "Timeout"
				if success {
					status = "OK"
				}
				fmt.Printf("%-20s | %-10s | %-12v | %d/%d\n", 
					host, status, latency.Round(time.Millisecond), i, *count)
				
				if i < *count {
					time.Sleep(1 * time.Second)
				}
			}
		}
		cancel()
	}

	select {
	case <-sigChan:
		fmt.Println("\nПолучен сигнал завершения. Остановка горутин...")
		cancel()
	case <-ctx.Done():
	}

	wg.Wait()
	fmt.Println("Программа успешно завершена.")
}

func pingHost(host string, useIcmp bool) (bool, time.Duration, error) {
	if useIcmp {
		return icmpPing(host)
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "80"), 2*time.Second)
	if err != nil {
		return false, 0, err
	}
	conn.Close()
	return true, time.Since(start), nil
}

func icmpPing(host string) (bool, time.Duration, error) {
	start := time.Now()
	c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return false, 0, err
	}
	defer c.Close()

	wm := icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{
			ID: os.Getpid() & 0xffff, Seq: 1,
			Data: []byte("PULSE"),
		},
	}
	wb, _ := wm.Marshal(nil)
	addr, _ := net.ResolveIPAddr("ip", host)
	c.WriteTo(wb, addr)

	rb := make([]byte, 1500)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := c.ReadFrom(rb)
	if err != nil {
		return false, 0, err
	}

	rm, _ := icmp.ParseMessage(ipv4.ICMPTypeEchoReply.Protocol(), rb[:n])
	return rm.Type == ipv4.ICMPTypeEchoReply, time.Since(start), nil
}

func readHosts(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var hosts []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if h := scanner.Text(); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts, scanner.Err()
}