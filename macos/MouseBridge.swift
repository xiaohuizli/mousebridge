import AppKit
import ApplicationServices
import Security
import Darwin

// The launcher owns its child process and exchanges secrets only over private
// environment/pipe channels. The identity lives only in memory for this launch.
final class BridgeProcess {
    let executable: URL
    private(set) var process: Process?
    var onOutput: ((Data) -> Void)?
    var onExit: ((Int32) -> Void)?
    private var input: Pipe?

    init(executable: URL) { self.executable = executable }

    func start(_ arguments: [String], key: String, trust: String) throws {
        guard process == nil else { throw NSError(domain: "MouseBridge", code: 1,
            userInfo: [NSLocalizedDescriptionKey: "请先停止当前会话。"] ) }
        let task = Process()
        task.executableURL = executable
        task.arguments = arguments
        var environment = ProcessInfo.processInfo.environment
        environment["MOUSEBRIDGE_KEY"] = key
        environment["MOUSEBRIDGE_TRUST"] = trust
        task.environment = environment
        let inputPipe = Pipe()
        input = inputPipe
        task.standardInput = inputPipe
        let pipe = Pipe()
        task.standardOutput = pipe
        task.standardError = pipe
        pipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            if data.isEmpty { handle.readabilityHandler = nil; return }
            DispatchQueue.main.async { self?.onOutput?(data) }
        }
        task.terminationHandler = { [weak self] finished in
            pipe.fileHandleForReading.readabilityHandler = nil
            let remainder = pipe.fileHandleForReading.readDataToEndOfFile()
            DispatchQueue.main.async {
                guard let self = self, self.process === finished else { return }
                if !remainder.isEmpty { self.onOutput?(remainder) }
                self.input = nil
                self.process = nil
                self.onExit?(finished.terminationStatus)
            }
        }
        process = task
        do { try task.run() } catch {
            pipe.fileHandleForReading.readabilityHandler = nil
            process = nil
            input = nil
            throw error
        }
    }

    func send(_ command: [String: Any]) throws {
        guard let handle = input?.fileHandleForWriting, process?.isRunning == true else { return }
        var data = try JSONSerialization.data(withJSONObject: command)
        data.append(10)
        try handle.write(contentsOf: data)
    }

    func stop() {
        guard let task = process, task.isRunning else { return }
        task.interrupt() // Go's interrupt context releases input and closes sockets.
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { [weak task] in
            if let task = task, task.isRunning { task.terminate() }
        }
    }
}

enum SessionIdentity {
    static func generate() throws -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        let status = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        guard status == errSecSuccess else {
            throw NSError(domain: NSOSStatusErrorDomain, code: Int(status), userInfo:
                [NSLocalizedDescriptionKey: "无法生成本次启动的临时身份。"])
        }
        return Data(bytes).base64EncodedString().replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }
}
struct LANDevice: Decodable {
    let id: String
    let name: String
}
struct AutoEvent: Decodable {
    let kind: String
    var id: String?
    var name: String?
    var role: String?
    var message: String?
    var peers: [LANDevice]?
    var selected: String?
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    let target = NSPopUpButton()
    let edge = NSPopUpButton()
    let discovery = NSButton(checkboxWithTitle: "开启局域网发现与双向共享", target: nil, action: nil)
    let device = NSTextField(labelWithString: "正在查找局域网电脑…")
    let status = NSTextField(wrappingLabelWithString: "正在启动…")
    let permission = NSTextField(wrappingLabelWithString: "")
    let authorize = NSButton(title: "启用鼠标权限…", target: nil, action: nil)
    let log = NSTextView()
    var identity: String?
    var devices: [LANDevice] = []
    var enabled = false
    var quitting = false
    var buffer = Data()
    lazy var worker = BridgeProcess(executable: Bundle.main.url(forResource: "mousebridge", withExtension: nil)!)

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        let menu = NSMenu()
        let item = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "关于 MouseBridge", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "退出 MouseBridge", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        item.submenu = appMenu
        menu.addItem(item)
        NSApp.mainMenu = menu
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 770, height: 620),
            styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "MouseBridge · 局域网互控"
        window.center()
        window.isReleasedWhenClosed = false
        let content = NSStackView()
        content.orientation = .vertical
        content.alignment = .leading
        content.spacing = 18
        content.translatesAutoresizingMaskIntoConstraints = false
        window.contentView!.addSubview(content)
        NSLayoutConstraint.activate([
            content.leadingAnchor.constraint(equalTo: window.contentView!.leadingAnchor, constant: 28),
            content.trailingAnchor.constraint(equalTo: window.contentView!.trailingAnchor, constant: -28),
            content.topAnchor.constraint(equalTo: window.contentView!.topAnchor, constant: 26),
            content.bottomAnchor.constraint(equalTo: window.contentView!.bottomAnchor, constant: -24)])
        let title = NSTextField(labelWithString: "同一局域网，打开即可互控")
        title.font = .systemFont(ofSize: 27, weight: .semibold)
        content.addArrangedSubview(title)
        content.addArrangedSubview(label("无需填写 IP、密钥或核对数字。每台电脑都可以控制其他电脑，也可以接收控制。"))
        discovery.state = UserDefaults.standard.bool(forKey: "lanDiscoveryOff") ? .off : .on
        discovery.target = self; discovery.action = #selector(discoveryChanged)
        content.addArrangedSubview(discovery)
        content.addArrangedSubview(label("开启后，局域网内其他同样开启此功能的电脑可自动连接并控制本机。"))
        device.font = .systemFont(ofSize: 16, weight: .medium)
        content.addArrangedSubview(device)
        target.addItem(withTitle: "等待发现电脑…")
        target.widthAnchor.constraint(equalToConstant: 310).isActive = true
        target.isEnabled = false
        target.target = self; target.action = #selector(targetChanged)
        edge.addItems(withTitles: ["右侧", "左侧"])
        edge.selectItem(at: UserDefaults.standard.bool(forKey: "autoEdgeLeft") ? 1 : 0)
        edge.target = self; edge.action = #selector(settingsChanged)
        content.addArrangedSubview(row("控制目标", [target, NSTextField(labelWithString: "对方在本机"), edge]))
        content.addArrangedSubview(label("两台电脑时自动选中对方；多台时可切换控制目标。穿过所选屏幕边缘开始控制，Esc 返回。"))
        permission.font = .systemFont(ofSize: 12)
        permission.textColor = .secondaryLabelColor
        permission.preferredMaxLayoutWidth = 710
        content.addArrangedSubview(permission)
        authorize.target = self; authorize.action = #selector(authorizeSharing)
        let access = NSButton(title: "辅助功能设置…", target: self, action: #selector(accessibilitySettings))
        let monitoring = NSButton(title: "输入监控设置…", target: self, action: #selector(inputSettings))
        for button in [authorize, access, monitoring] { button.bezelStyle = .rounded }
        content.addArrangedSubview(NSStackView(views: [authorize, access, monitoring]))
        status.font = .systemFont(ofSize: 12, weight: .medium)
        status.preferredMaxLayoutWidth = 710
        content.addArrangedSubview(status)
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        log.isEditable = false
        log.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
        log.textContainerInset = NSSize(width: 10, height: 10)
        log.isVerticallyResizable = true
        log.autoresizingMask = [.width]
        log.textContainer?.widthTracksTextView = true
        scroll.documentView = log
        content.addArrangedSubview(scroll)
        scroll.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
        scroll.heightAnchor.constraint(greaterThanOrEqualToConstant: 120).isActive = true
        worker.onOutput = { [weak self] in self?.consume($0) }
        worker.onExit = { [weak self] code in
            guard let self = self else { return }
            if self.quitting { NSApp.reply(toApplicationShouldTerminate: true); return }
            self.updateStatus("共享服务已退出（\(code)），重新开启发现可启动服务。")
        }
        refreshPermission()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        launch()
    }
    func label(_ value: String) -> NSTextField {
        let field = NSTextField(wrappingLabelWithString: value)
        field.font = .systemFont(ofSize: 12)
        field.textColor = .secondaryLabelColor
        field.preferredMaxLayoutWidth = 710
        return field
    }
    func row(_ title: String, _ fields: [NSView]) -> NSStackView {
        let caption = NSTextField(labelWithString: title)
        caption.widthAnchor.constraint(equalToConstant: 80).isActive = true
        let result = NSStackView(views: [caption] + fields)
        result.spacing = 10
        return result
    }
    var discover: Bool { discovery.state == .on }
    func launch() {
        guard worker.process == nil else { return }
        do {
            if identity == nil { identity = try SessionIdentity.generate() }
            guard let identity = identity else { return }
            enabled = discover && AXIsProcessTrusted()
            buffer.removeAll()
            var arguments = ["auto", "-open-lan", "-live", "-discover=\(discover)", "-edge", edge.indexOfSelectedItem == 0 ? "right" : "left"]
            if enabled { arguments.append("-enabled") }
            // Legacy device approvals are intentionally ignored in open LAN mode.
            try worker.start(arguments, key: identity, trust: "{}")
            updateStatus(discover ? "开放发现已开启，正在自动连接局域网电脑…" : "发现已关闭，禁止远端连接与鼠标共享。")
        } catch { updateStatus("无法启动：\(error.localizedDescription)") }
    }
    func refreshPermission() {
        permission.stringValue = AXIsProcessTrusted()
            ? "辅助功能已授权；向其他电脑发送本机鼠标还需输入监控权限。"
            : "自动发现可直接使用。实际控制与接收鼠标需要辅助功能权限，控制端还需输入监控权限。"
        authorize.isEnabled = discover
    }
    @objc func authorizeSharing() {
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
        guard AXIsProcessTrustedWithOptions(options) else {
            refreshPermission()
            updateStatus("请在系统设置中允许 MouseBridge 使用辅助功能，然后再次点击“启用鼠标权限”或重新打开应用。")
            return
        }
        if !CGPreflightListenEventAccess() { _ = CGRequestListenEventAccess() }
        enabled = discover
        if worker.process == nil { launch() }
        sendSettings()
        refreshPermission()
    }
    @objc func discoveryChanged() {
        UserDefaults.standard.set(!discover, forKey: "lanDiscoveryOff")
        enabled = discover && AXIsProcessTrusted()
        if worker.process == nil { launch() }
        sendSettings()
        refreshPermission()
        updateStatus(discover ? "开放发现已开启，将自动连接所有开启该功能的局域网电脑。" : "发现已关闭，正在结束全部远端控制与接收会话。")
    }
    @objc func settingsChanged() {
        UserDefaults.standard.set(edge.indexOfSelectedItem == 1, forKey: "autoEdgeLeft")
        sendSettings()
    }
    @objc func targetChanged() {
        let index = target.indexOfSelectedItem
        guard devices.indices.contains(index) else { return }
        do { try worker.send(["kind": "select", "id": devices[index].id]) }
        catch { updateStatus("无法切换目标：\(error.localizedDescription)") }
    }
    func sendSettings() {
        do { try worker.send(["kind": "settings", "discover": discover, "enabled": enabled,
                              "edge": edge.indexOfSelectedItem == 0 ? "right" : "left"]) }
        catch { updateStatus("设置未能发送：\(error.localizedDescription)") }
    }
    func consume(_ chunk: Data) {
        buffer.append(chunk)
        while let newline = buffer.firstIndex(of: 10) {
            let data = Data(buffer[..<newline])
            buffer.removeSubrange(...newline)
            if let event = try? JSONDecoder().decode(AutoEvent.self, from: data) { handle(event) }
            else if let text = String(data: data, encoding: .utf8), !text.isEmpty { appendLog(text + "\n") }
        }
        if buffer.count > 8192 { buffer.removeAll() }
    }
    func handle(_ event: AutoEvent) {
        switch event.kind {
        case "searching": updateStatus(discover ? "正在自动寻找局域网电脑，无需首次确认。" : "发现已关闭，禁止远端连接与鼠标共享。")
        case "devices":
            devices = event.peers ?? []
            target.removeAllItems()
            if devices.isEmpty {
                target.addItem(withTitle: "等待发现电脑…")
                target.isEnabled = false
                device.stringValue = discover ? "正在查找局域网电脑…" : "局域网发现已关闭"
            } else {
                for peer in devices { target.addItem(withTitle: "\(peer.name) · \(peer.id.prefix(6))") }
                if let index = devices.firstIndex(where: { $0.id == event.selected }) { target.selectItem(at: index) }
                target.isEnabled = discover
                device.stringValue = "已连接 \(devices.count) 台电脑 · 均可控制与接收"
            }
        case "connected": appendLog("已自动连接：\(event.name ?? "局域网电脑")\n")
        case "receiving": appendLog("可接收来自“\(event.name ?? "局域网电脑")”的鼠标控制。\n")
        case "controlling": updateStatus("控制目标：\(event.name ?? "局域网电脑")；其他在线电脑仍可向本机发送鼠标。")
        case "error":
            let message = event.message ?? "局域网共享遇到问题"
            if message.contains("Accessibility") {
                enabled = false; sendSettings()
                updateStatus("请为 MouseBridge 及系统列出的内置 mousebridge 程序开启辅助功能权限。")
            } else if message.contains("event tap") {
                updateStatus("接收保持开启；请授予输入监控权限，以便使用本机鼠标控制其他电脑。")
            } else { updateStatus(message) }
        default: break
        }
    }
    @objc func accessibilitySettings() { openSettings("Privacy_Accessibility") }
    @objc func inputSettings() { openSettings("Privacy_ListenEvent") }
    func openSettings(_ pane: String) {
        NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.preference.security?\(pane)")!)
    }
    func updateStatus(_ message: String) {
        if status.stringValue != message { status.stringValue = message; appendLog(message + "\n") }
    }
    func appendLog(_ message: String) {
        var safe = message
        if let identity = identity { safe = safe.replacingOccurrences(of: identity, with: "[身份已隐藏]") }
        log.textStorage?.append(NSAttributedString(string: safe, attributes: [.font: NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)]))
        if log.string.count > 32000 { log.string = String(log.string.suffix(24000)) }
        log.scrollToEndOfDocument(nil)
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard worker.process != nil else { return .terminateNow }
        quitting = true
        worker.stop()
        return .terminateLater
    }
}

signal(SIGPIPE, SIG_IGN)
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
