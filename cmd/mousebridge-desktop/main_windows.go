//go:build windows

// The Windows launcher uses only native Win32 controls and a private core pipe.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"mousebridge/internal/autopair"
	"mousebridge/internal/bridge"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const version = "0.3.1"
const className = "MouseBridgeDesktop"
const (
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmCommand      = 0x0111
	wmSetFont      = 0x0030
	wmEvent        = 0x8001
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	idDiscovery    = 1001
	idTarget       = 1002
	idEdge         = 1003
	bmGetCheck     = 0x00F0
	bmSetCheck     = 0x00F1
	cbAddString    = 0x0143
	cbGetCurSel    = 0x0147
	cbSetCurSel    = 0x014E
	cbResetContent = 0x014B
)

var user32 = syscall.NewLazyDLL("user32.dll")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var gdi32 = syscall.NewLazyDLL("gdi32.dll")
var createWindow = user32.NewProc("CreateWindowExW")
var defWindow = user32.NewProc("DefWindowProcW")
var sendMessage = user32.NewProc("SendMessageW")
var setText = user32.NewProc("SetWindowTextW")
var postMessage = user32.NewProc("PostMessageW")
var destroyWindow = user32.NewProc("DestroyWindow")
var enableWindow = user32.NewProc("EnableWindow")
var application *desktopApp

func text(value string) *uint16           { p, _ := syscall.UTF16PtrFromString(value); return p }
func ptr(value string) uintptr            { return uintptr(unsafe.Pointer(text(value))) }
func signed(value int32) uintptr          { return uintptr(value) }
func setLabel(hwnd uintptr, value string) { setText.Call(hwnd, ptr(value)) }

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type point struct{ X, Y int32 }
type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type preferences struct {
	DiscoveryOff bool `json:"discovery_off"`
	Left         bool `json:"left"`
}
type desktopApp struct {
	hwnd, discovery, target, edge, status, roster, log, font, titleFont uintptr
	peers                                                               []autopair.Device
	identity                                                            string
	prefs                                                               preferences
	worker                                                              *coreProcess
	queue                                                               chan autopair.Event
	closing                                                             bool
	logText                                                             string
}

func preferencePath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "MouseBridge", "preferences.json")
}
func loadPreferences() preferences {
	var prefs preferences
	if data, err := os.ReadFile(preferencePath()); err == nil {
		json.Unmarshal(data, &prefs)
	}
	return prefs
}
func (p preferences) save() error {
	path := preferencePath()
	if path == "" {
		return fmt.Errorf("无法读取用户设置目录")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(p)
	file, err := os.CreateTemp(filepath.Dir(path), "preferences-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (a *desktopApp) control(kind, title string, style uint32, x, y, w, h, id int) uintptr {
	handle, _, _ := createWindow.Call(0, ptr(kind), ptr(title), uintptr(wsChild|wsVisible|style), uintptr(x), uintptr(y), uintptr(w), uintptr(h), a.hwnd, uintptr(id), 0, 0)
	sendMessage.Call(handle, wmSetFont, a.font, 1)
	return handle
}
func (a *desktopApp) initialize() {
	a.font, _, _ = gdi32.NewProc("GetStockObject").Call(17)
	a.titleFont, _, _ = gdi32.NewProc("CreateFontW").Call(signed(-25), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 0, 0, ptr("Segoe UI"))
	title := a.control("STATIC", "同一局域网，打开即可互控", 0, 26, 24, 740, 40, 0)
	sendMessage.Call(title, wmSetFont, a.titleFont, 1)
	a.control("STATIC", "无需填写 IP、密钥或核对数字。每台电脑都可以控制其他电脑，也可以接收控制。", 0, 26, 72, 750, 38, 0)
	a.discovery = a.control("BUTTON", "开启局域网发现与双向共享", wsTabStop|3, 26, 115, 620, 28, idDiscovery)
	if !a.prefs.DiscoveryOff {
		sendMessage.Call(a.discovery, bmSetCheck, 1, 0)
	}
	a.control("STATIC", "开启后，同一局域网内其他同样开启此功能的电脑可自动连接并控制本机。", 0, 26, 153, 748, 36, 0)
	a.roster = a.control("STATIC", "正在查找局域网电脑…", 0, 26, 202, 748, 25, 0)
	a.control("STATIC", "控制目标", 0, 26, 246, 80, 25, 0)
	a.target = a.control("COMBOBOX", "", wsTabStop|0x0003|0x00200000, 110, 240, 380, 280, idTarget)
	sendMessage.Call(a.target, cbAddString, 0, ptr("等待发现电脑…"))
	sendMessage.Call(a.target, cbSetCurSel, 0, 0)
	enableWindow.Call(a.target, 0)
	a.control("STATIC", "对方在本机", 0, 510, 246, 100, 25, 0)
	a.edge = a.control("COMBOBOX", "", wsTabStop|0x0003, 615, 240, 135, 180, idEdge)
	sendMessage.Call(a.edge, cbAddString, 0, ptr("右侧"))
	sendMessage.Call(a.edge, cbAddString, 0, ptr("左侧"))
	var selection uintptr
	if a.prefs.Left {
		selection = 1
	}
	sendMessage.Call(a.edge, cbSetCurSel, selection, 0)
	a.control("STATIC", "两台电脑时自动选中对方，多台时可切换目标。穿过所选主屏幕边缘进入对方，Esc 返回。", 0, 26, 288, 748, 42, 0)
	a.control("STATIC", "无需管理员权限。若 Windows 提示网络访问，请根据当前局域网选择允许；关闭发现可结束全部共享。", 0, 26, 336, 748, 44, 0)
	a.status = a.control("STATIC", "正在启动局域网共享…", 0, 26, 387, 748, 36, 0)
	a.log = a.control("EDIT", "", 0x0004|0x0800|0x0040|0x00200000, 26, 436, 748, 130, 0)
}
func (a *desktopApp) appendLog(value string) {
	if a.identity != "" {
		value = strings.ReplaceAll(value, a.identity, "[临时身份已隐藏]")
	}
	a.logText += value + "\r\n"
	if len(a.logText) > 28000 {
		a.logText = string([]rune(a.logText)[len([]rune(a.logText))/2:])
	}
	setLabel(a.log, a.logText)
	sendMessage.Call(a.log, 0x00B1, ^uintptr(0), ^uintptr(0))
	sendMessage.Call(a.log, 0x00B7, 0, 0)
}
func (a *desktopApp) updateStatus(value string) { setLabel(a.status, value); a.appendLog(value) }
func (a *desktopApp) post(event autopair.Event) {
	a.queue <- event
	postMessage.Call(a.hwnd, wmEvent, 0, 0)
}
func (a *desktopApp) start() {
	identity, err := bridge.NewKey()
	if err != nil {
		a.updateStatus("无法生成临时身份：" + err.Error())
		return
	}
	a.identity = identity
	executable, err := os.Executable()
	if err != nil {
		a.updateStatus(err.Error())
		return
	}
	worker, err := startCore(filepath.Join(filepath.Dir(executable), "mousebridge-core.exe"), identity, !a.prefs.DiscoveryOff, a.prefs.Left, a.post)
	if err != nil {
		a.updateStatus("无法启动内置程序：" + err.Error())
		return
	}
	a.worker = worker
	if a.prefs.DiscoveryOff {
		a.updateStatus("发现已关闭，禁止远端控制与接收。")
	}
}
func (a *desktopApp) settings() {
	checked, _, _ := sendMessage.Call(a.discovery, bmGetCheck, 0, 0)
	edge, _, _ := sendMessage.Call(a.edge, cbGetCurSel, 0, 0)
	a.prefs.DiscoveryOff = checked != 1
	a.prefs.Left = edge == 1
	if err := a.prefs.save(); err != nil {
		a.appendLog("设置未保存：" + err.Error())
	}
	if a.worker == nil {
		a.start()
		return
	}
	selectedEdge := "right"
	if a.prefs.Left {
		selectedEdge = "left"
	}
	if err := a.worker.send(map[string]any{"kind": "settings", "discover": checked == 1, "enabled": checked == 1, "edge": selectedEdge}); err != nil {
		a.updateStatus("无法发送设置：" + err.Error())
	}
	if checked == 1 {
		a.updateStatus("开放发现已开启，将自动连接局域网电脑。")
	} else {
		a.updateStatus("发现已关闭，正在结束全部控制与接收会话。")
	}
}
func (a *desktopApp) handle(event autopair.Event) {
	switch event.Kind {
	case "devices":
		a.peers = event.Peers
		sendMessage.Call(a.target, cbResetContent, 0, 0)
		if len(a.peers) == 0 {
			sendMessage.Call(a.target, cbAddString, 0, ptr("等待发现电脑…"))
			sendMessage.Call(a.target, cbSetCurSel, 0, 0)
			enableWindow.Call(a.target, 0)
			if a.prefs.DiscoveryOff {
				setLabel(a.roster, "局域网发现已关闭")
			} else {
				setLabel(a.roster, "正在查找局域网电脑…")
			}
		} else {
			for i, peer := range a.peers {
				label := peer.Name + " · " + peer.ID[:6]
				sendMessage.Call(a.target, cbAddString, 0, ptr(label))
				if peer.ID == event.Selected {
					sendMessage.Call(a.target, cbSetCurSel, uintptr(i), 0)
				}
			}
			if a.prefs.DiscoveryOff {
				enableWindow.Call(a.target, 0)
			} else {
				enableWindow.Call(a.target, 1)
			}
			setLabel(a.roster, fmt.Sprintf("已连接 %d 台电脑 · 均可控制与接收", len(a.peers)))
		}
	case "searching":
		if !a.prefs.DiscoveryOff {
			a.updateStatus("正在自动寻找局域网电脑，无需首次确认。")
		}
	case "connected":
		a.appendLog("已自动连接：" + event.Name)
	case "receiving":
		a.appendLog("可接收来自“" + event.Name + "”的鼠标控制。")
	case "controlling":
		a.updateStatus("控制目标：" + event.Name + "；接收其他在线电脑保持可用。")
	case "error", "launcher_error":
		a.updateStatus(event.Message)
	case "log":
		a.appendLog(event.Message)
	case "service_exited":
		a.worker = nil
		if a.closing {
			destroyWindow.Call(a.hwnd)
		} else {
			a.updateStatus("共享服务已退出。重新开启发现可以再次启动。")
		}
	}
}
func windowProcedure(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	a := application
	switch msg {
	case wmCommand:
		id, notification := int(wparam&0xffff), int((wparam>>16)&0xffff)
		if id == idDiscovery && notification == 0 || id == idEdge && notification == 1 {
			a.settings()
			return 0
		}
		if id == idTarget && notification == 1 && a.worker != nil {
			index, _, _ := sendMessage.Call(a.target, cbGetCurSel, 0, 0)
			if index < uintptr(len(a.peers)) {
				if err := a.worker.send(map[string]any{"kind": "select", "id": a.peers[index].ID}); err != nil {
					a.updateStatus(err.Error())
				}
			}
			return 0
		}
	case wmEvent:
		for {
			select {
			case event := <-a.queue:
				a.handle(event)
			default:
				return 0
			}
		}
	case wmClose:
		if a.closing {
			return 0
		}
		a.closing = true
		enableWindow.Call(a.discovery, 0)
		enableWindow.Call(a.target, 0)
		enableWindow.Call(a.edge, 0)
		if a.worker == nil {
			destroyWindow.Call(hwnd)
		} else {
			a.updateStatus("正在释放鼠标并退出…")
			go a.worker.stop()
		}
		return 0
	case wmDestroy:
		user32.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	result, _, _ := defWindow.Call(hwnd, uintptr(msg), wparam, lparam)
	return result
}

// coreProcess never persists the private identity and exits cleanly on stdin EOF.
type coreProcess struct {
	cmd   *exec.Cmd
	input io.WriteCloser
	mu    sync.Mutex
	done  chan struct{}
}

func startCore(path, identity string, discover, left bool, emit func(autopair.Event)) (*coreProcess, error) {
	edge := "right"
	if left {
		edge = "left"
	}
	args := []string{"auto", "-open-lan", "-live", fmt.Sprintf("-discover=%t", discover), "-edge", edge}
	if discover {
		args = append(args, "-enabled")
	}
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	env := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "MOUSEBRIDGE_KEY=") && !strings.HasPrefix(value, "MOUSEBRIDGE_TRUST=") {
			env = append(env, value)
		}
	}
	cmd.Env = append(env, "MOUSEBRIDGE_KEY="+identity, "MOUSEBRIDGE_TRUST={}")
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		stderr.Close()
		return nil, err
	}
	core := &coreProcess{cmd: cmd, input: input, done: make(chan struct{})}
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 8192), 16384)
		for scanner.Scan() {
			var event autopair.Event
			if json.Unmarshal(scanner.Bytes(), &event) == nil {
				emit(event)
			}
		}
	}()
	go func() {
		defer readers.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			emit(autopair.Event{Kind: "log", Message: scanner.Text()})
		}
	}()
	go func() { readers.Wait(); cmd.Wait(); close(core.done); emit(autopair.Event{Kind: "service_exited"}) }()
	return core, nil
}
func (c *coreProcess) send(command any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.input == nil {
		return fmt.Errorf("共享服务已停止")
	}
	return json.NewEncoder(c.input).Encode(command)
}
func (c *coreProcess) stop() {
	c.mu.Lock()
	if c.input != nil {
		c.input.Close()
		c.input = nil
	}
	c.mu.Unlock()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		c.cmd.Process.Kill()
	}
}
func main() {
	runtime.LockOSThread()
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	mutex, _, mutexErr := kernel32.NewProc("CreateMutexW").Call(0, 0, ptr("Local\\MouseBridge.Desktop"))
	if mutex != 0 {
		defer syscall.CloseHandle(syscall.Handle(mutex))
	}
	if mutexErr == syscall.Errno(183) {
		existing, _, _ := user32.NewProc("FindWindowW").Call(ptr(className), 0)
		if existing != 0 {
			user32.NewProc("ShowWindow").Call(existing, 9)
			user32.NewProc("SetForegroundWindow").Call(existing)
		}
		return
	}
	app := &desktopApp{prefs: loadPreferences(), queue: make(chan autopair.Event, 256)}
	application = app
	cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
	icon, _, _ := user32.NewProc("LoadIconW").Call(0, 32512)
	class := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Proc: syscall.NewCallback(windowProcedure), Instance: instance, Cursor: cursor, Icon: icon, SmallIcon: icon, Background: 6, Name: text(className)}
	if registered, _, _ := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class))); registered == 0 {
		return
	}
	hwnd, _, _ := createWindow.Call(0, ptr(className), ptr("MouseBridge "+version+" · 局域网互控"), 0x00CA0000, 0x80000000, 0x80000000, 820, 650, 0, 0, instance, 0)
	if hwnd == 0 {
		return
	}
	app.hwnd = hwnd
	app.initialize()
	user32.NewProc("ShowWindow").Call(hwnd, 5)
	user32.NewProc("UpdateWindow").Call(hwnd)
	app.start()
	var msg message
	for {
		result, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		dialog, _, _ := user32.NewProc("IsDialogMessageW").Call(hwnd, uintptr(unsafe.Pointer(&msg)))
		if dialog == 0 {
			user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
			user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
	}
	if app.titleFont != 0 {
		gdi32.NewProc("DeleteObject").Call(app.titleFont)
	}
}
