package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type TimeOfDay struct {
	Hour   int
	Minute int
}

var (
	idleTreshold = parseIdleTreshold(os.Getenv("IDLE_TIME"), 120)
	workdaysSet  = parseWorkdays(os.Getenv("WORK_DAYS"))
	workStart    = parseTimeOfDay(os.Getenv("WORK_START"), TimeOfDay{8, 0})
	workEnd      = parseTimeOfDay(os.Getenv("WORK_END"), TimeOfDay{17, 0})
	logs         = parseLogPath(os.Getenv("LOG_PATH"), "/var/logs")
)

func parseLogPath(input string, def string) string {
	if input == "" {
		return def
	}
	return input
}

func parseWorkdays(input string) map[time.Weekday]bool {
	result := make(map[time.Weekday]bool)
	if input == "" {
		input = "Mon,Tue,Wed,Thu,Fri"
	}
	parts := strings.Split(input, ",")
	for _, p := range parts {
		switch strings.TrimSpace(strings.ToLower(p)) {
		case "mon":
			result[time.Monday] = true
		case "tue":
			result[time.Tuesday] = true
		case "wed":
			result[time.Wednesday] = true
		case "thu":
			result[time.Thursday] = true
		case "fri":
			result[time.Friday] = true
		case "sat":
			result[time.Saturday] = true
		case "sun":
			result[time.Sunday] = true
		}
	}
	return result
}

func parseTimeOfDay(input string, def TimeOfDay) TimeOfDay {
	if input == "" {
		return def
	}
	t, err := time.Parse("15:04", input)
	if err != nil {
		return def
	}
	return TimeOfDay{t.Hour(), t.Minute()}
}

func parseIdleTreshold(input string, def int) int {
	if input == "" {
		return def
	}
	val, err := strconv.Atoi(input)
	if err != nil {
		fmt.Println("Could not convert idleTreshold to int", err)
		return def
	}
	return val
}

func runAppleScript(script string) (string, error) {
	cmd := exec.Command("osascript", "-e", script)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func getFrontAppInfo() (appName, bundleID string, err error) {
	appName, err = runAppleScript(`tell application "System Events" to get name of first process whose frontmost is true`)
	if err != nil {
		return
	}
	bundleID, _ = runAppleScript(`id of application (path to frontmost application as text)`)
	return
}

func getWindowTitle(appProcessName string) (string, error) {
	script := fmt.Sprintf(`tell application "System Events" to tell process "%s" to get value of attribute "AXTitle" of window 1`, appProcessName)
	return runAppleScript(script)
}

func getIdleSeconds() int {
	cmd := exec.Command("bash", "-c", `ioreg -c IOHIDSystem | awk '/HIDIdleTime/ {print int($NF/1000000000); exit}'`)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	idleStr := strings.TrimSpace(string(out))
	idle, _ := strconv.Atoi(idleStr)
	return idle
}

// Work hours: Mon–Fri, 08:00–17:00
func isWorkHour(now time.Time) bool {
	// If it's an overnight window, the "workday" check is a bit subjective.
	// Here we treat the day by the *start* segment: e.g., 22:00–23:59 belongs to today's workday,
	// and 00:00–06:00 belongs to *the same* window continuing into tomorrow.
	// If you want to restrict weekdays strictly, keep this simple check:
	if !workdaysSet[now.Weekday()] {
		// If window crosses midnight, allow early-morning segment to count toward the *previous* day's workday.
		if crossesMidnight(workStart, workEnd) {
			yesterday := now.AddDate(0, 0, -1).Weekday()
			if !workdaysSet[yesterday] {
				return false
			}
		} else {
			return false
		}
	}

	start := time.Date(now.Year(), now.Month(), now.Day(), workStart.Hour, workStart.Minute, 0, 0, now.Location())
	end := time.Date(now.Year(), now.Month(), now.Day(), workEnd.Hour, workEnd.Minute, 0, 0, now.Location())

	if !crossesMidnight(workStart, workEnd) {
		return !now.Before(start) && now.Before(end)
	}

	endTomorrow := end.Add(24 * time.Hour)
	if now.Hour() >= workStart.Hour || (now.Hour() == workStart.Hour && now.Minute() >= workStart.Minute) {
		return true
	}

	nowPlusDay := now.Add(24 * time.Hour)
	return nowPlusDay.Before(endTomorrow)
}

func crossesMidnight(a, b TimeOfDay) bool {
	// True if start > end, e.g., 22:00–06:00
	if a.Hour > b.Hour {
		return true
	}
	if a.Hour == b.Hour && a.Minute > b.Minute {
		return true
	}
	return false
}

// Parse duration string like "3h5m2s" or "45m0s"
func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err == nil {
		return d
	}
	// fallback: manually parse "1h2m3s" patterns
	var total time.Duration
	re := regexp.MustCompile(`(\d+)h`)
	if h := re.FindStringSubmatch(s); len(h) == 2 {
		hrs, _ := strconv.Atoi(h[1])
		total += time.Duration(hrs) * time.Hour
	}
	re = regexp.MustCompile(`(\d+)m`)
	if m := re.FindStringSubmatch(s); len(m) == 2 {
		mins, _ := strconv.Atoi(m[1])
		total += time.Duration(mins) * time.Minute
	}
	re = regexp.MustCompile(`(\d+)s`)
	if sec := re.FindStringSubmatch(s); len(sec) == 2 {
		secs, _ := strconv.Atoi(sec[1])
		total += time.Duration(secs) * time.Second
	}
	return total
}

// Read an existing log and merge totals into the given map
func readExistingLog(totals map[string]map[string]time.Duration, suffix string) {
	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logs, fmt.Sprintf("focus_tracker_%s%s.log", dateStr, suffix))
	f, err := os.Open(logPath)
	if err != nil {
		return // file not found -> nothing to merge
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var currentApp string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Focus Summary") {
			continue
		}

		if strings.Contains(line, "—") && !strings.HasPrefix(line, "-") {
			// App line — header only, do not import as data
			parts := strings.SplitN(line, "—", 2)
			if len(parts) == 2 {
				currentApp = strings.TrimSpace(parts[0])
			}
			continue // ✅ skip adding duration here
		}

		if strings.HasPrefix(line, "-") {
			// Title line: "- title: duration"
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && currentApp != "" {
				title := strings.TrimSpace(strings.TrimPrefix(parts[0], "-"))
				durStr := strings.TrimSpace(parts[1])
				d := parseDuration(durStr)
				if _, ok := totals[currentApp]; !ok {
					totals[currentApp] = make(map[string]time.Duration)
				}
				totals[currentApp][title] += d
			}
		}
	}
	fmt.Printf("↻ Loaded previous totals from %s\n", logPath)
}

// Save the totals to a file (normal or outside hours)
func saveSummaryToFile(totals map[string]map[string]time.Duration, suffix string) {
	if len(totals) == 0 {
		return
	}

	dateStr := time.Now().Format("2006-01-02")
	filename := fmt.Sprintf("focus_tracker_%s%s.log", dateStr, suffix)
	logPath := filepath.Join(logs, filename)

	writeSummary := func(w io.Writer) {
		fmt.Fprintf(w, "Focus Summary for %s (%s)\n", dateStr, suffix)
		fmt.Fprintf(w, "----------------------------------------\n")

		for app, titleMap := range totals {
			var totalApp time.Duration
			for _, d := range titleMap {
				totalApp += d
			}
			fmt.Fprintf(w, "%s — %v\n", app, totalApp.Round(time.Second))
			for title, d := range titleMap {
				if title == "" {
					title = "(no title)"
				}
				fmt.Fprintf(w, "  - %s: %v\n", title, d.Round(time.Second))
			}
		}
		fmt.Fprintln(w)
	}

	// Try writing to file
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Printf("⚠️ Could not write log file %s: %v\n", logPath, err)
		fmt.Println("---- Printing summary to stdout instead ----")
		writeSummary(os.Stdout)
		return
	}
	defer f.Close()

	writeSummary(f)
	fmt.Printf("✅ Summary written to %s\n", logPath)
}

func main() {
	var lastApp, lastTitle string
	lastSwitch := time.Now()

	workTotals := make(map[string]map[string]time.Duration)
	outsideTotals := make(map[string]map[string]time.Duration)

	// Load previous sessions for today
	readExistingLog(workTotals, "")
	readExistingLog(outsideTotals, "_outside")

	fmt.Println("Tracking focus... Press Ctrl+C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sig
		fmt.Println("\n\n=== Final Summary ===")
		saveSummaryToFile(workTotals, "")
		saveSummaryToFile(outsideTotals, "_outside")
		os.Exit(0)
	}()

	lastKnownTitle := make(map[string]string)

	for {
		idle := getIdleSeconds()
		now := time.Now()

		// Locked screen handling
		if idle > idleTreshold {
			if lastApp != "Locked screen" {
				duration := time.Since(lastSwitch)
				if lastApp != "" {
					var totals map[string]map[string]time.Duration
					if isWorkHour(lastSwitch) {
						totals = workTotals
					} else {
						totals = outsideTotals
					}
					if _, ok := totals[lastApp]; !ok {
						totals[lastApp] = make(map[string]time.Duration)
					}
					totals[lastApp][lastTitle] += duration
				}

				lockStart := now.Format("15:04:05")
				lockStart = strings.ReplaceAll(lockStart, ":", "-")
				fmt.Printf("%s [%s]: active for %v\n", lastApp, lastTitle, duration.Round(time.Second))

				lastApp = "Locked screen"
				lastTitle = lockStart
				lastSwitch = now
			}
			time.Sleep(5 * time.Second)
			continue
		}

		appName, bundleID, err := getFrontAppInfo()
		if err != nil || appName == "" {
			time.Sleep(2 * time.Second)
			continue
		}

		// Handle VS Code's Electron quirk
		appProcessName := appName
		if bundleID == "com.microsoft.VSCode" {
			appName = "Visual Studio Code"
			appProcessName = "Electron"
		}

		title, _ := getWindowTitle(appProcessName)
		if appName == "Visual Studio Code" {
			title = strings.TrimSuffix(title, " — Visual Studio Code")
		}

		if title == "" {
			// use cached last known title if available
			if prev, ok := lastKnownTitle[appName]; ok && prev != "" {
				title = prev
			}
		} else {
			// update cache with new non-empty title
			lastKnownTitle[appName] = title
		}

		// Focus changed
		if appName != lastApp || title != lastTitle {
			duration := time.Since(lastSwitch)
			if lastApp != "" {
				var totals map[string]map[string]time.Duration
				if isWorkHour(lastSwitch) {
					totals = workTotals
				} else {
					totals = outsideTotals
				}
				if _, ok := totals[lastApp]; !ok {
					totals[lastApp] = make(map[string]time.Duration)
				}
				totals[lastApp][lastTitle] += duration
				fmt.Printf("%s [%s]: active for %v\n", lastApp, lastTitle, duration.Round(time.Second))
			}

			lastApp = appName
			lastTitle = title
			lastSwitch = now
		}

		// Autosave every 10 minutes
		if now.Minute()%10 == 0 && now.Second() < 2 {
			saveSummaryToFile(workTotals, "")
			saveSummaryToFile(outsideTotals, "_outside")
		}

		time.Sleep(2 * time.Second)
	}
}
