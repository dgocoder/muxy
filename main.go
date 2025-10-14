package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	tcellterm "github.com/dgocoder/muxy/tcell-term"
	"github.com/gdamore/tcell/v2"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Title     string    `yaml:"title,omitempty"`
	Splash    string    `yaml:"splash,omitempty"`
	Processes []Process `yaml:"processes"`
}

type Process struct {
	Name        string            `yaml:"name"`
	Command     string            `yaml:"command"`
	Directory   string            `yaml:"directory,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Color       string            `yaml:"color,omitempty"`
	Autostart   *bool             `yaml:"autostart,omitempty"`
}

var SIDEBAR_WIDTH = 30

type pane struct {
	name          string
	command       string
	dir           string
	env           []string
	color         tcell.Color
	vt            *tcellterm.VT
	dead          bool
	exitCode      int
	manualKill    bool
	statusMessage string
	cmd           *exec.Cmd
	autostart     bool
	notStarted    bool
}

type Multiplexer struct {
	focused   bool
	width     int
	height    int
	selected  int
	processes []*pane
	screen    tcell.Screen
	ctx       context.Context
	cancel    context.CancelFunc
	splash    []string
	title     []string
}

var colorMap = map[string]tcell.Color{
	"green":   tcell.ColorGreen,
	"yellow":  tcell.ColorYellow,
	"blue":    tcell.ColorBlue,
	"magenta": tcell.ColorPurple,
	"cyan":    tcell.ColorTeal,
	"white":   tcell.ColorWhite,
	"gray":    tcell.ColorGray,
	"purple":  tcell.ColorPurple,
}

var defaultColors = []tcell.Color{
	tcell.ColorTeal,
	tcell.ColorGreen,
	tcell.ColorYellow,
	tcell.ColorBlue,
	tcell.ColorPurple,
	tcell.ColorPurple,
}

func New(config Config) (*Multiplexer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Default splash if none provided
	defaultSplash := []string{
		" __  __  _   _ __  ____   __",
		"|  \\/  || | | |\\ \\/ /\\ \\ / /",
		"| |\\/| || | | | \\  /  \\ V / ",
		"| |  | || |_| | /  \\   | |  ",
		"|_|  |_| \\___/ /_/\\_\\  |_|  ",
	}

	// Load splash from file if provided, otherwise use default
	var splashLines []string
	if config.Splash != "" {
		data, err := os.ReadFile(config.Splash)
		if err == nil {
			lines := strings.Split(string(data), "\n")
			// Trim trailing empty lines
			for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
				lines = lines[:len(lines)-1]
			}
			splashLines = lines
		} else {
			// If file doesn't exist or can't be read, use default
			splashLines = defaultSplash
		}
	} else {
		// No splash file specified, use default
		splashLines = defaultSplash
	}

	var titleLines []string
	if config.Title != "" {
		titleLines = []string{
			strings.ToUpper(config.Title),
		}
	} else {
		// Default to MUXY if no title provided
		titleLines = []string{
			"MUXY",
		}
	}

	result := &Multiplexer{
		processes: []*pane{},
		ctx:       ctx,
		cancel:    cancel,
		splash:    splashLines,
		title:     titleLines,
	}

	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := screen.Init(); err != nil {
		return nil, err
	}
	screen.EnableMouse()
	screen.Clear()

	result.screen = screen
	width, height := screen.Size()
	result.width = width
	result.height = height

	// Create panes from config
	for i, proc := range config.Processes {
		env := os.Environ()
		for key, value := range proc.Environment {
			env = append(env, fmt.Sprintf("%s=%s", key, value))
		}

		color := colorMap[proc.Color]
		if color == 0 {
			color = defaultColors[i%len(defaultColors)]
		}

		// Default autostart to true if not specified
		autostart := true
		if proc.Autostart != nil {
			autostart = *proc.Autostart
		}

		p := &pane{
			name:       proc.Name,
			command:    proc.Command,
			dir:        proc.Directory,
			env:        env,
			color:      color,
			autostart:  autostart,
			notStarted: !autostart,
		}

		// Create terminal emulator
		term := tcellterm.New()
		p.vt = term

		result.processes = append(result.processes, p)
	}

	return result, nil
}

func (p *pane) start() error {
	args := []string{"sh", "-c", p.command}
	p.cmd = exec.Command(args[0], args[1:]...)
	p.cmd.Env = p.env
	if p.dir != "" {
		p.cmd.Dir = p.dir
	}
	// Only clear terminal on first start (when vt is new)
	// Keep output visible when process exits
	err := p.vt.Start(p.cmd)
	if err != nil {
		return err
	}
	p.dead = false
	p.notStarted = false
	p.exitCode = 0
	p.manualKill = false
	p.statusMessage = ""
	return nil
}

func (p *pane) restart() error {
	// Clear the terminal for restart
	p.vt.Clear()
	return p.start()
}

func (p *pane) Kill() {
	p.killWithMessage(true)
}

func (p *pane) killQuiet() {
	p.killWithMessage(false)
}

func (p *pane) killWithMessage(showMessage bool) {
	p.manualKill = true

	// Set a status message that will be displayed in the terminal view
	if showMessage {
		p.statusMessage = "▸ Process manually stopped"
	}

	if p.cmd != nil && p.cmd.Process != nil {
		// Send SIGTERM
		p.cmd.Process.Signal(syscall.SIGTERM)

		// Wait up to 500ms for graceful shutdown
		done := make(chan bool)
		go func() {
			p.cmd.Wait()
			done <- true
		}()

		select {
		case <-done:
			// Process terminated gracefully
		case <-time.After(500 * time.Millisecond):
			// Force kill if not terminated
			p.cmd.Process.Kill()
		}
	}
	p.vt.Close()
}

func (s *Multiplexer) selectedProcess() *pane {
	if s.selected >= 0 && s.selected < len(s.processes) {
		return s.processes[s.selected]
	}
	return nil
}

func (s *Multiplexer) move(offset int) {
	s.selected = (s.selected + offset) % len(s.processes)
	if s.selected < 0 {
		s.selected = len(s.processes) - 1
	}
	s.draw()
}

func (s *Multiplexer) focus() {
	s.focused = true
	s.draw()
}

func (s *Multiplexer) blur() {
	s.focused = false
	selected := s.selectedProcess()
	if selected != nil {
		selected.vt.ScrollReset()
	}
	s.draw()
}

func (s *Multiplexer) draw() {
	s.screen.Clear()

	// Draw title header
	titleLines := s.title
	y := 0
	if len(titleLines) > 0 {
		titleStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen).Bold(true)
		for i, line := range titleLines {
			// Left-align the text with a small indent
			if line != "" {
				for x, r := range line {
					if x+1 < SIDEBAR_WIDTH {
						s.screen.SetContent(x+1, i, r, nil, titleStyle)
					}
				}
			}
		}
		y = len(titleLines)
	}

	// Group processes into running and stopped
	var runningProcs []*pane
	var stoppedProcs []*pane

	for _, p := range s.processes {
		if !p.dead && !p.notStarted {
			runningProcs = append(runningProcs, p)
		} else {
			stoppedProcs = append(stoppedProcs, p)
		}
	}

	// Draw running processes first
	for _, p := range runningProcs {
		// Find actual index in s.processes for selection check
		actualIdx := -1
		for idx, proc := range s.processes {
			if proc == p {
				actualIdx = idx
				break
			}
		}

		style := tcell.StyleDefault
		if actualIdx == s.selected {
			style = style.Background(tcell.ColorDarkSlateGray).Bold(true)
			// Fill entire row with background color
			for x := 0; x < SIDEBAR_WIDTH; x++ {
				s.screen.SetContent(x, y, ' ', nil, style)
			}
		}

		// Running process - always green filled circle
		status := "●"
		statusStyle := style.Foreground(p.color)
		nameStyle := style.Foreground(p.color)

		// Draw status
		for x, r := range status {
			s.screen.SetContent(x+1, y, r, nil, statusStyle)
		}

		// Draw name
		name := p.name
		if len(name) > SIDEBAR_WIDTH-5 {
			name = name[:SIDEBAR_WIDTH-5]
		}
		for x, r := range name {
			s.screen.SetContent(x+3, y, r, nil, nameStyle)
		}

		y++
	}

	// Draw separator if we have both running and stopped processes
	if len(runningProcs) > 0 && len(stoppedProcs) > 0 {
		separatorStyle := tcell.StyleDefault.Foreground(tcell.ColorGray).Dim(true)
		for x := 0; x < SIDEBAR_WIDTH; x++ {
			s.screen.SetContent(x, y, '─', nil, separatorStyle)
		}
		y++
	}

	// Draw stopped/not-started processes
	for _, p := range stoppedProcs {
		// Find actual index in s.processes for selection check
		actualIdx := -1
		for idx, proc := range s.processes {
			if proc == p {
				actualIdx = idx
				break
			}
		}

		style := tcell.StyleDefault
		if actualIdx == s.selected {
			style = style.Background(tcell.ColorDarkSlateGray).Bold(true)
			// Fill entire row with background color
			for x := 0; x < SIDEBAR_WIDTH; x++ {
				s.screen.SetContent(x, y, ' ', nil, style)
			}
		}

		// Status indicator based on state
		status := "◯"
		var statusStyle, nameStyle tcell.Style

		if p.notStarted {
			// Not started - hollow circle, dimmed
			status = "◯"
			statusStyle = style.Foreground(p.color).Dim(true)
			nameStyle = style.Foreground(p.color).Dim(true)
		} else if p.dead && !p.manualKill && p.exitCode != 0 {
			// Exited with error (not manually killed) - red X
			status = "✗"
			statusStyle = style.Foreground(tcell.ColorRed).Bold(true)
			nameStyle = style.Foreground(tcell.ColorRed)
		} else if p.dead {
			// Exited cleanly or manually killed - dimmed circle
			status = "○"
			statusStyle = style.Foreground(p.color).Dim(true)
			nameStyle = style.Foreground(p.color).Dim(true)
		}

		// Draw status
		for x, r := range status {
			s.screen.SetContent(x+1, y, r, nil, statusStyle)
		}

		// Draw name
		name := p.name
		if len(name) > SIDEBAR_WIDTH-5 {
			name = name[:SIDEBAR_WIDTH-5]
		}
		for x, r := range name {
			s.screen.SetContent(x+3, y, r, nil, nameStyle)
		}

		y++
	}

	// Draw help text at bottom of sidebar
	// Determine the enter key action based on selected process state
	enterAction := "enter: focus"
	selected := s.selectedProcess()
	if selected != nil {
		if selected.notStarted {
			enterAction = "enter: start"
		} else if selected.dead {
			enterAction = "enter: restart"
		}
	}

	helpLines := []string{
		"──────────────────────────────",
		"tab: switch process",
		"up/down or j/k: navigate",
		"u/d or pgup/pgdn: scroll",
		enterAction,
		"x: kill process",
		"q/ctrl+c: quit",
	}

	if s.focused {
		helpLines = []string{
			"──────────────────────────────",
			"🔴 focused mode",
			"",
			"pgup/pgdn: scroll",
			"ctrl+z: unfocus",
			"",
			"all other keys are",
			"sent to the process",
		}
	}

	helpStartY := s.height - len(helpLines)
	if helpStartY < y {
		helpStartY = y
	}
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray).Dim(true)
	for i, line := range helpLines {
		if i == 0 {
			// Separator line
			for x, r := range line {
				if x < SIDEBAR_WIDTH {
					s.screen.SetContent(x, helpStartY+i, r, nil, helpStyle)
				}
			}
		} else {
			// Help text
			for x, r := range line {
				if x < SIDEBAR_WIDTH {
					s.screen.SetContent(x, helpStartY+i, r, nil, helpStyle)
				}
			}
		}
	}

	// Draw separator
	for y := 0; y < s.height; y++ {
		s.screen.SetContent(SIDEBAR_WIDTH, y, '│', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
	}

	// Draw terminal for selected process (reuse selected from above)
	if selected != nil {
		// Position terminal - now using full height
		mainWidth := s.width - SIDEBAR_WIDTH - 1
		mainHeight := s.height - 1 // One line for title
		selected.vt.Resize(mainWidth, mainHeight)

		// Draw title with status
		title := fmt.Sprintf(" %s ", selected.name)
		titleStyle := tcell.StyleDefault.Foreground(selected.color).Bold(true)

		if selected.notStarted {
			title = fmt.Sprintf(" %s [NOT STARTED - Press Enter to start] ", selected.name)
			titleStyle = tcell.StyleDefault.Foreground(selected.color).Dim(true).Bold(true)
		} else if selected.dead && !selected.manualKill && selected.exitCode != 0 {
			title = fmt.Sprintf(" %s [EXITED WITH ERROR (code %d) - Press Enter to restart] ", selected.name, selected.exitCode)
			titleStyle = tcell.StyleDefault.Foreground(tcell.ColorRed).Bold(true)
		} else if selected.dead {
			title = fmt.Sprintf(" %s [EXITED - Press Enter to restart] ", selected.name)
			titleStyle = tcell.StyleDefault.Foreground(selected.color).Dim(true).Bold(true)
		}
		for x, r := range title {
			if x+SIDEBAR_WIDTH+1 < s.width {
				s.screen.SetContent(x+SIDEBAR_WIDTH+1, 0, r, nil, titleStyle)
			}
		}

		// Draw terminal content (even if dead, to show final output)
		selected.vt.Draw()

		// Draw status message at the bottom if present
		if selected.statusMessage != "" {
			mainHeight := s.height - 1
			statusY := mainHeight        // Bottom of the terminal area
			statusX := SIDEBAR_WIDTH + 2 // Offset from sidebar

			// Yellow/warning color for status message
			statusStyle := tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true).Background(tcell.ColorBlack)
			for i, r := range selected.statusMessage {
				if statusX+i < s.width {
					s.screen.SetContent(statusX+i, statusY, r, nil, statusStyle)
				}
			}
		}
	}

	s.screen.Show()
}

func (s *Multiplexer) resize(width, height int) {
	s.width = width
	s.height = height
	mainWidth := width - SIDEBAR_WIDTH - 1
	mainHeight := height - 1 // One line for title
	for _, p := range s.processes {
		p.vt.Resize(mainWidth, mainHeight)
	}
	s.draw()
}

func (s *Multiplexer) showSplashWithMessage(message string) {
	// Skip splash if none configured
	if len(s.splash) == 0 {
		return
	}

	s.screen.Clear()

	// Add message to splash with controlled spacing
	splashLines := append(s.splash, "", message)

	// Calculate center position
	centerY := (s.height - len(splashLines)) / 2
	style := tcell.StyleDefault.Foreground(tcell.ColorGreen).Bold(true)
	messageStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)

	for i, line := range splashLines {
		y := centerY + i
		if y >= 0 && y < s.height {
			centerX := (s.width - len(line)) / 2
			lineStyle := style
			if i == len(splashLines)-1 {
				lineStyle = messageStyle
			}
			for x, r := range line {
				if centerX+x >= 0 && centerX+x < s.width {
					s.screen.SetContent(centerX+x, y, r, nil, lineStyle)
				}
			}
		}
	}

	s.screen.Show()
}

func (s *Multiplexer) showSplash() {
	s.showSplashWithMessage("    Loading processes...")
}

func (s *Multiplexer) Start() error {
	defer func() {
		s.screen.Fini()
	}()

	// Show splash screen
	s.showSplash()

	// Start all processes (only those with autostart enabled)
	for _, p := range s.processes {
		// Set up terminal surface to render at correct position
		offsetX := SIDEBAR_WIDTH + 1
		offsetY := 1 // Start after title line
		p.vt.SetSurface(&offsetSurface{
			screen:  s.screen,
			offsetX: offsetX,
			offsetY: offsetY,
		})

		// Attach event handler
		p.vt.Attach(func(ev tcell.Event) {
			s.screen.PostEvent(ev)
		})

		// Give a small delay to ensure terminal is ready
		time.Sleep(50 * time.Millisecond)

		// Start process only if autostart is enabled
		if p.autostart {
			if err := p.start(); err != nil {
				// Silently fail - user will see it in the terminal output
			}
		}
	}

	s.draw()
	s.screen.Show()

	// Event loop
	for {
		ev := s.screen.PollEvent()
		if ev == nil {
			continue
		}

		selected := s.selectedProcess()

		switch evt := ev.(type) {
		case *tcellterm.EventRedraw:
			if selected != nil && selected.vt == evt.VT() {
				selected.vt.Draw()
				s.screen.Show()
			}

		case *tcellterm.EventClosed:
			for _, proc := range s.processes {
				if proc.vt == evt.VT() {
					proc.dead = true
					// Capture exit code if available
					if proc.cmd != nil && proc.cmd.ProcessState != nil {
						proc.exitCode = proc.cmd.ProcessState.ExitCode()
					} else if proc.cmd != nil && !proc.manualKill {
						// If ProcessState is not available and wasn't manually killed,
						// assume it crashed (exit code 1)
						proc.exitCode = 1
					}
					// Auto-unfocus if the closed process is currently focused
					if s.focused && selected != nil && selected == proc {
						s.blur()
					}
					s.draw()
				}
			}

		case *tcell.EventResize:
			s.resize(evt.Size())
			s.screen.Sync()

		case *tcell.EventMouse:
			// Handle mouse wheel scrolling (works in both focused and unfocused mode)
			if selected != nil {
				x, _ := evt.Position()
				// Only handle scroll if in main terminal area
				if x > SIDEBAR_WIDTH {
					switch evt.Buttons() {
					case tcell.WheelUp:
						selected.vt.ScrollUp(3)
						selected.vt.Draw()
						s.screen.Show()
					case tcell.WheelDown:
						selected.vt.ScrollDown(3)
						selected.vt.Draw()
						s.screen.Show()
					}
				}
			}

		case *tcell.EventKey:
			// Handle special keys when not focused
			if !s.focused {
				switch evt.Key() {
				case tcell.KeyTab, tcell.KeyRight:
					s.move(1)
					continue
				case tcell.KeyBacktab, tcell.KeyLeft:
					s.move(-1)
					continue
				case tcell.KeyUp:
					s.move(-1)
					continue
				case tcell.KeyDown:
					s.move(1)
					continue
				case tcell.KeyPgUp:
					if selected != nil {
						selected.vt.ScrollUp(10)
						selected.vt.Draw()
						s.screen.Show()
					}
					continue
				case tcell.KeyPgDn:
					if selected != nil {
						selected.vt.ScrollDown(10)
						selected.vt.Draw()
						s.screen.Show()
					}
					continue
				case tcell.KeyEnter:
					if selected != nil {
						if selected.notStarted {
							// Start process that hasn't been started yet
							if err := selected.start(); err != nil {
								// Show error in terminal (could enhance this)
							}
							s.draw()
						} else if selected.dead {
							// Restart dead process
							if err := selected.restart(); err != nil {
								// Show error in terminal (could enhance this)
							}
							s.draw()
						} else {
							// Focus into running process
							s.focus()
						}
					}
					continue
				case tcell.KeyCtrlC:
					s.showSplashWithMessage("    Terminating processes...")
					// Kill all processes while splash is showing (quietly, no individual messages)
					for _, p := range s.processes {
						if !p.dead {
							p.killQuiet()
						}
					}
					time.Sleep(300 * time.Millisecond)
					return nil
				case tcell.KeyRune:
					switch evt.Rune() {
					case 'q':
						s.showSplashWithMessage("    Terminating processes...")
						// Kill all processes while splash is showing (quietly, no individual messages)
						for _, p := range s.processes {
							if !p.dead {
								p.killQuiet()
							}
						}
						time.Sleep(300 * time.Millisecond)
						return nil
					case 'j':
						s.move(1)
						continue
					case 'k':
						s.move(-1)
						continue
					case 'u':
						// Scroll up in terminal (vim-style)
						if selected != nil {
							selected.vt.ScrollUp(5)
							selected.vt.Draw()
							s.screen.Show()
						}
						continue
					case 'd':
						// Scroll down in terminal (vim-style)
						if selected != nil {
							selected.vt.ScrollDown(5)
							selected.vt.Draw()
							s.screen.Show()
						}
						continue
					case 'x':
						if selected != nil && !selected.dead {
							selected.Kill()
						}
						continue
					}
				}
			}

			// Handle keys when focused
			if s.focused {
				switch evt.Key() {
				case tcell.KeyCtrlZ:
					s.blur()
					continue
				case tcell.KeyPgUp:
					// Allow scrolling in focused mode
					if selected != nil {
						selected.vt.ScrollUp(10)
						selected.vt.Draw()
						s.screen.Show()
					}
					continue
				case tcell.KeyPgDn:
					// Allow scrolling in focused mode
					if selected != nil {
						selected.vt.ScrollDown(10)
						selected.vt.Draw()
						s.screen.Show()
					}
					continue
				}

				// Pass all other keys to the terminal
				if selected != nil && !selected.dead {
					selected.vt.HandleEvent(evt)
					s.draw()
				}
			}
		}
	}
}

// offsetSurface wraps tcell.Screen to offset all drawing operations
type offsetSurface struct {
	screen  tcell.Screen
	offsetX int
	offsetY int
}

func (s *offsetSurface) SetContent(x, y int, mainc rune, combc []rune, style tcell.Style) {
	s.screen.SetContent(x+s.offsetX, y+s.offsetY, mainc, combc, style)
}

func (s *offsetSurface) GetContent(x, y int) (rune, []rune, tcell.Style, int) {
	return s.screen.GetContent(x+s.offsetX, y+s.offsetY)
}

func (s *offsetSurface) SetStyle(style tcell.Style) {
	s.screen.SetStyle(style)
}

func (s *offsetSurface) ShowCursor(x, y int) {
	s.screen.ShowCursor(x+s.offsetX, y+s.offsetY)
}

func (s *offsetSurface) HideCursor() {
	s.screen.HideCursor()
}

func (s *offsetSurface) Size() (int, int) {
	w, h := s.screen.Size()
	return w - s.offsetX, h - s.offsetY
}

// expandEnvVars replaces ${VAR} and ${VAR:-default} patterns with environment variable values
func expandEnvVars(s string) string {
	// Regex pattern to match ${VAR} or ${VAR:-default}
	re := regexp.MustCompile(`\$\{([^}]+)\}`)

	return re.ReplaceAllStringFunc(s, func(match string) string {
		// Extract content between ${ and }
		content := match[2 : len(match)-1]

		// Check if it has a default value (VAR:-default)
		var varName string
		var defaultValue string
		var hasDefault bool

		if idx := strings.Index(content, ":-"); idx != -1 {
			varName = content[:idx]
			defaultValue = content[idx+2:]
			hasDefault = true
		} else {
			varName = content
		}

		// Get the environment variable value
		if value, exists := os.LookupEnv(varName); exists && value != "" {
			return value
		}

		// Return default value if provided
		if hasDefault {
			return defaultValue
		}

		// Return original if variable doesn't exist and no default
		return match
	})
}

// expandConfigEnvVars expands environment variables in all string fields of the config
func expandConfigEnvVars(config *Config) {
	// Expand title and splash
	config.Title = expandEnvVars(config.Title)
	config.Splash = expandEnvVars(config.Splash)

	// Expand each process's fields
	for i := range config.Processes {
		proc := &config.Processes[i]
		proc.Name = expandEnvVars(proc.Name)
		proc.Command = expandEnvVars(proc.Command)
		proc.Directory = expandEnvVars(proc.Directory)
		proc.Color = expandEnvVars(proc.Color)

		// Expand environment variable values
		for key, value := range proc.Environment {
			proc.Environment[key] = expandEnvVars(value)
		}
	}
}

func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Expand environment variables in the config
	expandConfigEnvVars(&config)

	return &config, nil
}

func main() {
	// Suppress all logs from SST's process library
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	configFile := "muxy.yml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	config, err := loadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if len(config.Processes) == 0 {
		fmt.Fprintf(os.Stderr, "No processes defined in config file\n")
		os.Exit(1)
	}

	mux, err := New(*config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating multiplexer: %v\n", err)
		os.Exit(1)
	}

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		mux.showSplashWithMessage("    Terminating processes...")
		for _, p := range mux.processes {
			if !p.dead {
				p.killQuiet()
			}
		}
		time.Sleep(300 * time.Millisecond)
		mux.screen.Fini()
		os.Exit(0)
	}()

	if err := mux.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// No need for cleanup here - processes already killed before exiting event loop
}
