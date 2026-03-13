import AppKit
import Foundation

enum AppLanguage {
    case zh
    case en
}

enum AppLanguagePreference: String {
    case auto
    case zh
    case en
}

private var configuredAppLanguagePreference: AppLanguagePreference = .auto

func setAppLanguagePreference(_ raw: String) {
    switch raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
    case "zh", "zh-cn", "zh-hans", "zh-hant", "cn", "中文", "chinese":
        configuredAppLanguagePreference = .zh
    case "en", "en-us", "en-gb", "english":
        configuredAppLanguagePreference = .en
    default:
        configuredAppLanguagePreference = .auto
    }
}

func resolvedAppLanguage() -> AppLanguage {
    switch configuredAppLanguagePreference {
    case .zh:
        return .zh
    case .en:
        return .en
    case .auto:
        let preferred = Locale.preferredLanguages.first?.lowercased() ?? Locale.current.identifier.lowercased()
        return preferred.hasPrefix("zh") ? .zh : .en
    }
}

func L(_ zh: String, _ en: String) -> String {
    resolvedAppLanguage() == .zh ? zh : en
}

func whisperSourceLabel(_ raw: String?) -> String {
    switch raw?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
    case "app-managed":
        return L("应用管理", "App-managed")
    case "homebrew":
        return "Homebrew"
    case "external":
        return L("外部安装", "External")
    default:
        return ""
    }
}

func bundledAppIcon() -> NSImage? {
    if let iconPath = Bundle.main.path(forResource: "AppIcon", ofType: "icns"),
       let image = NSImage(contentsOfFile: iconPath) {
        return image
    }

    if let fallback = NSImage(systemSymbolName: "paperplane.circle.fill", accessibilityDescription: L("Telegram Codex Bridge", "Telegram Codex Bridge")) {
        return fallback
    }

    return nil
}

struct BridgePaths: Decodable {
    let project_root: String
    let bridge_binary: String
    let bridge_control: String
    let env_file: String
    let state_path: String
    let logs_dir: String
    let stdout_log: String
    let stderr_log: String
    let launch_agents_dir: String
    let plist_path: String
}

struct CodexHealth: Decodable {
    let configured_binary: String
    let resolved_binary: String?
    let app_detected: Bool
    let found: Bool
    let version: String?
    let logged_in: Bool
    let login_status: String?
    let ready: Bool
    let error: String?
}

struct CodexLimitWindow: Decodable {
    let used_percent: Int
    let remaining_percent: Int
    let limit_window_seconds: Int64
    let reset_after_seconds: Int64
    let reset_at: Int64
}

struct CodexLimits: Decodable {
    let available: Bool
    let error: String?
    let plan_type: String?
    let primary_window: CodexLimitWindow?
    let secondary_window: CodexLimitWindow?
    let fetched_at: Int64?
}

struct BridgeStatus: Decodable {
    let label: String
    let version: String?
    let installed: Bool
    let loaded: Bool
    let running: Bool
    let auto_start: Bool
    let pid: Int?
    let unmanaged_pid: Int?
    let last_exit: Int?
    let description: String?
    let paths: BridgePaths
    let codex: CodexHealth?
    let permission_mode: String?
}

struct WhisperStatus: Decodable {
    let available: Bool
    let installed: Bool
    let source: String?
    let whisper_path: String?
    let python_path: String?
    let ffmpeg_path: String?
    let version: String?
    let model: String?
    let install_root: String?
    let error: String?
}

struct WhisperInstallResult: Decodable {
    let status: WhisperStatus
    let output: String?
}

struct BridgeConfig {
    var botToken: String = ""
    var allowedUserIDs: String = ""
    var allowedChatIDs: String = ""
    var workspaceRoot: String = ""
    var codexBinary: String = ""
    var language: String = "auto"
    var permissionMode: String = "default"
    var autoStart: Bool = true

    var isConfigured: Bool {
        let hasToken = !botToken.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        let hasWorkspace = !workspaceRoot.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        let hasRouting = !allowedUserIDs.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
            !allowedChatIDs.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        return hasToken && hasWorkspace && hasRouting
    }
}

final class BridgeConfigStore {
    let runtimeRoot: String
    let envPath: String
    let binDir: String
    let legacyHelperBinaryPath: String
    let helperBinaryPath: String
    let bridgeBinaryPath: String

    init() {
        if let overrideRoot = ProcessInfo.processInfo.environment["TELEGRAM_CODEX_BRIDGE_ROOT"], !overrideRoot.isEmpty {
            runtimeRoot = overrideRoot
        } else {
            let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
            runtimeRoot = base.appendingPathComponent("TelegramCodexBridge", isDirectory: true).path
        }

        envPath = (runtimeRoot as NSString).appendingPathComponent(".env")
        binDir = (runtimeRoot as NSString).appendingPathComponent("bin")
        bridgeBinaryPath = (binDir as NSString).appendingPathComponent("telegram-codex-bridge")
        helperBinaryPath = bridgeBinaryPath
        legacyHelperBinaryPath = (binDir as NSString).appendingPathComponent("bridgectl")
    }

    func prepareRuntime() throws {
        try FileManager.default.createDirectory(atPath: runtimeRoot, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: binDir, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: (runtimeRoot as NSString).appendingPathComponent("data/logs"), withIntermediateDirectories: true)

        try syncBundledBinary(named: "telegram-codex-bridge", to: bridgeBinaryPath)
        if FileManager.default.fileExists(atPath: legacyHelperBinaryPath) {
            _ = try? FileManager.default.removeItem(atPath: legacyHelperBinaryPath)
        }
    }

    func loadConfig() -> BridgeConfig? {
        guard let content = try? String(contentsOfFile: envPath, encoding: .utf8) else {
            return nil
        }

        var config = BridgeConfig()
        for rawLine in content.components(separatedBy: .newlines) {
            let line = rawLine.trimmingCharacters(in: .whitespacesAndNewlines)
            if line.isEmpty || line.hasPrefix("#") || !line.contains("=") {
                continue
            }

            let parts = line.split(separator: "=", maxSplits: 1).map(String.init)
            guard parts.count == 2 else { continue }

            let key = parts[0].trimmingCharacters(in: .whitespacesAndNewlines)
            let value = parts[1].trimmingCharacters(in: .whitespacesAndNewlines)
            switch key {
            case "TELEGRAM_BOT_TOKEN":
                config.botToken = value
            case "TELEGRAM_ALLOWED_USER_IDS":
                config.allowedUserIDs = value
            case "TELEGRAM_ALLOWED_CHAT_IDS":
                config.allowedChatIDs = value
            case "CODEX_WORKSPACE_ROOT":
                config.workspaceRoot = value
            case "CODEX_BIN":
                config.codexBinary = value
            case "BRIDGE_LANGUAGE":
                config.language = value
            case "CODEX_PERMISSION_MODE":
                config.permissionMode = value
            default:
                break
            }
        }

        return config
    }

    func needsSetup() -> Bool {
        guard let config = loadConfig() else {
            return true
        }
        return !config.isConfigured
    }

    func saveConfig(_ config: BridgeConfig) throws {
        let lines = [
            "TELEGRAM_BOT_TOKEN=\(config.botToken)",
            "TELEGRAM_ALLOWED_USER_IDS=\(config.allowedUserIDs)",
            "TELEGRAM_ALLOWED_CHAT_IDS=\(config.allowedChatIDs)",
            "CODEX_WORKSPACE_ROOT=\(config.workspaceRoot)",
            "CODEX_BIN=\(config.codexBinary)",
            "BRIDGE_LANGUAGE=\(config.language)",
            "CODEX_PERMISSION_MODE=\(config.permissionMode)",
        ]
        try lines.joined(separator: "\n").appending("\n").write(toFile: envPath, atomically: true, encoding: .utf8)
    }

    private func syncBundledBinary(named name: String, to destination: String) throws {
        let candidate = Bundle.main.path(forResource: name, ofType: nil)
        guard let source = candidate else {
            throw NSError(domain: "BridgeStatusBarApp", code: 1, userInfo: [NSLocalizedDescriptionKey: L("缺少打包资源 \(name)", "Missing bundled resource \(name)")])
        }

        if FileManager.default.fileExists(atPath: destination) {
            _ = try? FileManager.default.removeItem(atPath: destination)
        }
        try FileManager.default.copyItem(atPath: source, toPath: destination)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: destination)
        if let icon = bundledAppIcon() {
            _ = NSWorkspace.shared.setIcon(icon, forFile: destination, options: [])
        }
    }
}

final class BridgeHelper {
    let projectRoot: String
    let helperPath: String

    init(projectRoot: String, helperPath: String) {
        self.projectRoot = projectRoot
        self.helperPath = helperPath
    }

    func status() throws -> BridgeStatus {
        let output = try run(["status", "--json"])
        return try JSONDecoder().decode(BridgeStatus.self, from: Data(output.utf8))
    }

    func codexStatus() throws -> CodexHealth {
        let output = try run(["codex", "--json"])
        return try JSONDecoder().decode(CodexHealth.self, from: Data(output.utf8))
    }

    func limits() throws -> CodexLimits {
        let output = try run(["limits", "--json"])
        return try JSONDecoder().decode(CodexLimits.self, from: Data(output.utf8))
    }

    func validateTelegramToken(_ token: String) throws {
        let output = try run(["telegram-check", "--json", "--token", token])
        let result = try JSONDecoder().decode(TelegramTokenHealth.self, from: Data(output.utf8))
        if !result.valid {
            throw NSError(
                domain: "BridgeStatusBarApp",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: result.error ?? L("Telegram token 校验失败", "Telegram token validation failed")]
            )
        }
    }

    func whisperStatus() throws -> WhisperStatus {
        let output = try run(["whisper-status", "--json"])
        return try JSONDecoder().decode(WhisperStatus.self, from: Data(output.utf8))
    }

    func installWhisper() throws -> WhisperInstallResult {
        let output = try run(["install-whisper", "--json"])
        let result = try JSONDecoder().decode(WhisperInstallResult.self, from: Data(output.utf8))
        if !result.status.installed {
            throw NSError(
                domain: "BridgeStatusBarApp",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: result.output ?? result.status.error ?? L("OpenAI Whisper 仍未安装成功。", "OpenAI Whisper is still not installed.")]
            )
        }
        return result
    }

    func start() throws {
        _ = try run(["start"])
    }

    func stop() throws {
        _ = try run(["stop"])
    }

    func restart() throws {
        _ = try run(["restart"])
    }

    func setAutostart(_ enabled: Bool) throws {
        _ = try run(["set-autostart", enabled ? "on" : "off"])
    }

    func stopUnmanaged() throws {
        _ = try run(["stop-unmanaged"])
    }

    private func run(_ arguments: [String]) throws -> String {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: helperPath)
        process.arguments = arguments + ["--project-root", projectRoot]

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe

        try process.run()
        process.waitUntilExit()

        let stdout = String(data: stdoutPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        let stderr = String(data: stderrPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""

        guard process.terminationStatus == 0 else {
            let message = stderr.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? stdout : stderr
            throw NSError(
                domain: "BridgeStatusBarApp",
                code: Int(process.terminationStatus),
                userInfo: [NSLocalizedDescriptionKey: message.trimmingCharacters(in: .whitespacesAndNewlines)]
            )
        }

        return stdout
    }
}

struct TelegramTokenHealth: Decodable {
    let api_base_url: String
    let valid: Bool
    let bot_id: Int64?
    let username: String?
    let error: String?
}

final class SetupWindowController: NSWindowController {
    private let codexProvider: () throws -> CodexHealth
    private let whisperProvider: () throws -> WhisperStatus
    private let installWhisperHandler: () async throws -> WhisperInstallResult
    private let saveHandler: (BridgeConfig) async throws -> Void

    private let tokenField = NSSecureTextField()
    private let workspaceField = NSTextField()
    private let userIDsField = NSTextField()
    private let chatIDsField = NSTextField()
    private let languagePopup = NSPopUpButton()
    private let permissionPopup = NSPopUpButton()
    private let autoStartCheckbox = NSButton(checkboxWithTitle: L("开机自动启动 Bridge", "Launch Bridge at login"), target: nil, action: nil)
    private let statusLabel = NSTextField(labelWithString: L("Codex 状态检测中…", "Checking Codex status…"))
    private let whisperStatusLabel = NSTextField(labelWithString: L("Whisper 状态检测中…", "Checking Whisper status…"))
    private let saveButton = NSButton(title: L("保存并启动", "Save and Start"), target: nil, action: nil)
    private let cancelButton = NSButton(title: L("稍后", "Later"), target: nil, action: nil)
    private let chooseWorkspaceButton = NSButton(title: L("选择目录", "Choose Folder"), target: nil, action: nil)
    private let refreshCodexButton = NSButton(title: L("重新检测 Codex", "Re-check Codex"), target: nil, action: nil)
    private let openCodexButton = NSButton(title: L("打开 Codex", "Open Codex"), target: nil, action: nil)
    private let installWhisperButton = NSButton(title: L("安装 OpenAI Whisper", "Install OpenAI Whisper"), target: nil, action: nil)

    private var latestCodexHealth: CodexHealth?
    private var latestWhisperStatus: WhisperStatus?
    private var whisperInstallTimer: Timer?
    private var whisperInstallElapsedSeconds = 0
    private var isInstallingWhisper = false

    init(
        existingConfig: BridgeConfig?,
        codexProvider: @escaping () throws -> CodexHealth,
        whisperProvider: @escaping () throws -> WhisperStatus,
        installWhisperHandler: @escaping () async throws -> WhisperInstallResult,
        saveHandler: @escaping (BridgeConfig) async throws -> Void
    ) {
        self.codexProvider = codexProvider
        self.whisperProvider = whisperProvider
        self.installWhisperHandler = installWhisperHandler
        self.saveHandler = saveHandler

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 640, height: 510),
            styleMask: [.titled, .closable],
            backing: .buffered,
            defer: false
        )
        window.title = L("Telegram Codex Bridge 设置", "Telegram Codex Bridge Setup")
        window.isReleasedWhenClosed = false

        super.init(window: window)
        configureUI()
        if let existingConfig {
            applyConfig(existingConfig)
        }
        refreshCodexHealth()
        refreshWhisperStatus()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    private func configureUI() {
        tokenField.placeholderString = "Telegram Bot Token"
        workspaceField.placeholderString = L("Codex 默认项目目录", "Default Codex project directory")
        userIDsField.placeholderString = L("私聊允许的 user id，多个用逗号分隔", "Allowed private-chat user IDs, comma-separated")
        chatIDsField.placeholderString = L("群组允许的 chat id，多个用逗号分隔", "Allowed group chat IDs, comma-separated")
        configureLanguagePopup()
        configurePermissionPopup()
        autoStartCheckbox.state = .on

        statusLabel.maximumNumberOfLines = 3
        statusLabel.lineBreakMode = .byWordWrapping
        whisperStatusLabel.maximumNumberOfLines = 3
        whisperStatusLabel.lineBreakMode = .byWordWrapping

        saveButton.target = self
        saveButton.action = #selector(saveAndStart)
        cancelButton.target = self
        cancelButton.action = #selector(closeWindow)
        chooseWorkspaceButton.target = self
        chooseWorkspaceButton.action = #selector(selectWorkspace)
        refreshCodexButton.target = self
        refreshCodexButton.action = #selector(refreshCodexHealthAction)
        openCodexButton.target = self
        openCodexButton.action = #selector(openCodex)
        installWhisperButton.target = self
        installWhisperButton.action = #selector(installWhisperAction)

        let content = NSView(frame: NSRect(x: 0, y: 0, width: 640, height: 465))
        content.translatesAutoresizingMaskIntoConstraints = false
        window?.contentView = content

        let hintLabel = NSTextField(labelWithString: L("首次运行需要完成基本配置。私聊 user id 和群 chat id 至少填一个。", "Finish the basic setup before first use. You must fill at least one of private user IDs or group chat IDs."))
        hintLabel.maximumNumberOfLines = 2
        hintLabel.lineBreakMode = .byWordWrapping

        let workspaceRow = horizontalStack([
            workspaceField,
            chooseWorkspaceButton,
        ])
        let codexRow = horizontalStack([
            statusLabel,
            refreshCodexButton,
            openCodexButton,
        ])
        let whisperRow = horizontalStack([
            whisperStatusLabel,
            installWhisperButton,
        ])
        let buttonsRow = horizontalStack([
            NSView(),
            cancelButton,
            saveButton,
        ])

        let stack = NSStackView(views: [
            hintLabel,
            labeledRow("Bot Token", tokenField),
            labeledRow(L("项目目录", "Project Directory"), workspaceRow),
            labeledRow(L("私聊 User IDs", "Private User IDs"), userIDsField),
            labeledRow(L("群 Chat IDs", "Group Chat IDs"), chatIDsField),
            labeledRow(L("语言", "Language"), languagePopup),
            labeledRow(L("Codex 权限", "Codex Permission"), permissionPopup),
            autoStartCheckbox,
            labeledRow("Codex", codexRow),
            labeledRow("Whisper", whisperRow),
            buttonsRow,
        ])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 12
        stack.translatesAutoresizingMaskIntoConstraints = false

        content.addSubview(stack)

        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            stack.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),
            stack.topAnchor.constraint(equalTo: content.topAnchor, constant: 20),
            stack.bottomAnchor.constraint(lessThanOrEqualTo: content.bottomAnchor, constant: -20),
            workspaceField.widthAnchor.constraint(greaterThanOrEqualToConstant: 320),
            tokenField.widthAnchor.constraint(greaterThanOrEqualToConstant: 320),
            userIDsField.widthAnchor.constraint(greaterThanOrEqualToConstant: 320),
            chatIDsField.widthAnchor.constraint(greaterThanOrEqualToConstant: 320),
        ])
    }

    private func applyConfig(_ config: BridgeConfig) {
        tokenField.stringValue = config.botToken
        workspaceField.stringValue = config.workspaceRoot
        userIDsField.stringValue = config.allowedUserIDs
        chatIDsField.stringValue = config.allowedChatIDs
        selectLanguage(config.language)
        selectPermissionMode(config.permissionMode)
        autoStartCheckbox.state = config.autoStart ? .on : .off
    }

    func updateConfig(_ config: BridgeConfig?) {
        if let config {
            applyConfig(config)
        }
    }

    func refreshDiagnostics() {
        refreshCodexHealth()
        refreshWhisperStatus()
    }

    @objc private func saveAndStart() {
        let selectedLanguage = currentLanguagePreference()
        setAppLanguagePreference(selectedLanguage)

        let config = BridgeConfig(
            botToken: tokenField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
            allowedUserIDs: userIDsField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
            allowedChatIDs: chatIDsField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
            workspaceRoot: workspaceField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
            codexBinary: latestCodexHealth?.resolved_binary ?? "",
            language: selectedLanguage,
            permissionMode: currentPermissionMode(),
            autoStart: autoStartCheckbox.state == .on
        )

        guard config.isConfigured else {
            showAlert(title: L("配置不完整", "Incomplete Configuration"), message: L("Bot Token、项目目录，以及私聊 user id / 群 chat id 至少一项需要填写。", "Bot token, project directory, and at least one of private user IDs or group chat IDs are required."))
            return
        }

        guard latestCodexHealth?.ready == true else {
            showAlert(title: L("Codex 未就绪", "Codex Not Ready"), message: latestCodexHealth?.error ?? latestCodexHealth?.login_status ?? L("请先安装并登录 Codex。", "Please install and sign in to Codex first."))
            return
        }

        saveButton.isEnabled = false
        statusLabel.stringValue = L("正在校验 Telegram Token 并写入配置…", "Validating Telegram token and writing configuration…")

        Task {
            do {
                try await saveHandler(config)
                await MainActor.run {
                    self.window?.orderOut(nil)
                    self.saveButton.isEnabled = true
                }
            } catch {
                await MainActor.run {
                    self.saveButton.isEnabled = true
                    self.showAlert(title: L("保存失败", "Save Failed"), message: error.localizedDescription)
                    self.statusLabel.stringValue = latestCodexHealth?.login_status ?? latestCodexHealth?.error ?? L("Codex 状态未知", "Codex status unknown")
                }
            }
        }
    }

    @objc private func closeWindow() {
        window?.orderOut(nil)
    }

    @objc private func selectWorkspace() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = false
        panel.allowsMultipleSelection = false
        if panel.runModal() == .OK {
            workspaceField.stringValue = panel.url?.path ?? ""
        }
    }

    @objc private func refreshCodexHealthAction() {
        refreshCodexHealth()
    }

    @objc private func openCodex() {
        let codexApp = URL(fileURLWithPath: "/Applications/Codex.app")
        if FileManager.default.fileExists(atPath: codexApp.path) {
            NSWorkspace.shared.open(codexApp)
        } else {
            showAlert(title: L("未找到 Codex.app", "Codex.app Not Found"), message: L("请先安装 Codex，然后回来点击“重新检测 Codex”。", "Please install Codex first, then come back and click “Re-check Codex”."))
        }
    }

    @objc private func installWhisperAction() {
        guard !isInstallingWhisper else {
            return
        }

        beginWhisperInstallProgress()

        Task {
            do {
                let result = try await installWhisperHandler()
                await MainActor.run {
                    self.latestWhisperStatus = result.status
                    self.endWhisperInstallProgress(installed: result.status.installed)
                    self.applyWhisperStatus(result.status)
                    if !result.status.installed {
                        self.showAlert(
                            title: L("安装未完成", "Installation Incomplete"),
                            message: result.output ?? result.status.error ?? L("Whisper 安装流程已执行，但当前仍未就绪。", "The Whisper installation completed, but Whisper is still not ready.")
                        )
                    }
                }
            } catch {
                await MainActor.run {
                    self.endWhisperInstallProgress(installed: false)
                    self.showAlert(title: L("安装失败", "Installation Failed"), message: error.localizedDescription)
                    self.refreshWhisperStatus()
                }
            }
        }
    }

    private func refreshCodexHealth() {
        statusLabel.stringValue = L("正在检测 Codex…", "Checking Codex…")
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                let health = try self.codexProvider()
                DispatchQueue.main.async {
                    self.latestCodexHealth = health
                    if health.ready {
                        self.statusLabel.stringValue = L("已检测到 Codex，", "Codex detected, ") + (health.login_status ?? L("已登录", "signed in"))
                    } else {
                        self.statusLabel.stringValue = health.error ?? health.login_status ?? L("Codex 未就绪", "Codex not ready")
                    }
                    if let whisperStatus = self.latestWhisperStatus {
                        self.applyWhisperStatus(whisperStatus)
                    }
                }
            } catch {
                DispatchQueue.main.async {
                    self.latestCodexHealth = nil
                    self.statusLabel.stringValue = error.localizedDescription
                    if let whisperStatus = self.latestWhisperStatus {
                        self.applyWhisperStatus(whisperStatus)
                    } else {
                        self.installWhisperButton.isEnabled = false
                    }
                }
            }
        }
    }

    private func refreshWhisperStatus() {
        if isInstallingWhisper {
            return
        }
        whisperStatusLabel.stringValue = L("正在检测 Whisper…", "Checking Whisper…")
        installWhisperButton.isEnabled = false
        installWhisperButton.title = L("安装 OpenAI Whisper", "Install OpenAI Whisper")
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                let status = try self.whisperProvider()
                DispatchQueue.main.async {
                    self.latestWhisperStatus = status
                    self.applyWhisperStatus(status)
                }
            } catch {
                DispatchQueue.main.async {
                    self.latestWhisperStatus = nil
                    self.whisperStatusLabel.stringValue = error.localizedDescription
                    self.installWhisperButton.isEnabled = true
                }
            }
        }
    }

    private func applyWhisperStatus(_ status: WhisperStatus) {
        latestWhisperStatus = status
        if isInstallingWhisper {
            return
        }
        if status.installed {
            let version = status.version?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let source = whisperSourceLabel(status.source)
            if version.isEmpty {
                whisperStatusLabel.stringValue = L("已安装，可用于语音转文字", "Installed and ready for speech-to-text") + (source.isEmpty ? "" : " (\(source))")
            } else {
                whisperStatusLabel.stringValue = L("已安装，可用于语音转文字", "Installed and ready for speech-to-text") + " (\(version)\(source.isEmpty ? "" : ", \(source)"))"
            }
            installWhisperButton.title = L("语音转文字功能已安装", "Speech-to-text is installed")
            installWhisperButton.isEnabled = false
            return
        }

        whisperStatusLabel.stringValue = status.error ?? L("未安装，可点击安装。首次使用可能会下载模型。", "Not installed. Click install. The first use may still download a model.")
        installWhisperButton.title = L("安装 OpenAI Whisper", "Install OpenAI Whisper")
        installWhisperButton.isEnabled = true
    }

    private func beginWhisperInstallProgress() {
        isInstallingWhisper = true
        whisperInstallElapsedSeconds = 0
        whisperStatusLabel.stringValue = L("正在安装 OpenAI Whisper… 优先检测现有安装，否则创建私有运行环境。", "Installing OpenAI Whisper… Existing installs are detected first, otherwise a private runtime will be created.")
        installWhisperButton.isEnabled = false
        updateWhisperInstallButtonTitle()

        whisperInstallTimer?.invalidate()
        whisperInstallTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            guard let self else { return }
            self.whisperInstallElapsedSeconds += 1
            self.updateWhisperInstallButtonTitle()
        }
    }

    private func endWhisperInstallProgress(installed: Bool) {
        isInstallingWhisper = false
        whisperInstallTimer?.invalidate()
        whisperInstallTimer = nil
        whisperInstallElapsedSeconds = 0
        if installed {
            installWhisperButton.title = L("语音转文字功能已安装", "Speech-to-text is installed")
            installWhisperButton.isEnabled = false
        } else {
            installWhisperButton.title = L("安装 OpenAI Whisper", "Install OpenAI Whisper")
            installWhisperButton.isEnabled = true
        }
    }

    private func updateWhisperInstallButtonTitle() {
        installWhisperButton.title = L("正在后台安装中 ", "Installing in background ") + "\(whisperInstallElapsedSeconds)s"
    }

    private func showAlert(title: String, message: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = title
        alert.informativeText = message
        alert.icon = bundledAppIcon()
        alert.addButton(withTitle: L("好的", "OK"))
        alert.runModal()
    }

    private func labeledRow(_ title: String, _ view: NSView) -> NSStackView {
        let label = NSTextField(labelWithString: title)
        label.alignment = .right
        label.font = .systemFont(ofSize: NSFont.systemFontSize)
        label.widthAnchor.constraint(equalToConstant: 110).isActive = true

        let stack = NSStackView(views: [label, view])
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.spacing = 10
        return stack
    }

    private func horizontalStack(_ views: [NSView]) -> NSStackView {
        let stack = NSStackView(views: views)
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.spacing = 10
        return stack
    }

    private func configureLanguagePopup() {
        languagePopup.removeAllItems()
        languagePopup.addItem(withTitle: L("跟随系统", "Auto / Follow System"))
        languagePopup.lastItem?.representedObject = "auto"
        languagePopup.addItem(withTitle: "中文")
        languagePopup.lastItem?.representedObject = "zh"
        languagePopup.addItem(withTitle: "English")
        languagePopup.lastItem?.representedObject = "en"
    }

    private func configurePermissionPopup() {
        permissionPopup.removeAllItems()
        permissionPopup.addItem(withTitle: L("默认", "Default"))
        permissionPopup.lastItem?.representedObject = "default"
        permissionPopup.addItem(withTitle: L("完全访问", "Full Access"))
        permissionPopup.lastItem?.representedObject = "full-access"
    }

    private func currentLanguagePreference() -> String {
        guard let item = languagePopup.selectedItem,
              let value = item.representedObject as? String else {
            return "auto"
        }
        return value
    }

    private func currentPermissionMode() -> String {
        guard let item = permissionPopup.selectedItem,
              let value = item.representedObject as? String else {
            return "default"
        }
        return value
    }

    private func selectLanguage(_ raw: String) {
        let normalized: String
        switch raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "zh", "zh-cn", "zh-hans", "zh-hant":
            normalized = "zh"
        case "en", "en-us", "en-gb":
            normalized = "en"
        default:
            normalized = "auto"
        }

        for item in languagePopup.itemArray {
            if let value = item.representedObject as? String, value == normalized {
                languagePopup.select(item)
                return
            }
        }
        languagePopup.selectItem(at: 0)
    }

    private func selectPermissionMode(_ raw: String) {
        let normalized: String
        switch raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "full", "full-access", "danger", "danger-full-access":
            normalized = "full-access"
        default:
            normalized = "default"
        }

        for item in permissionPopup.itemArray {
            if let value = item.representedObject as? String, value == normalized {
                permissionPopup.select(item)
                return
            }
        }
        permissionPopup.selectItem(at: 0)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let menu = NSMenu()
    private let configStore = BridgeConfigStore()

    private var helper: BridgeHelper!
    private var setupWindowController: SetupWindowController?

    private lazy var summaryItem = NSMenuItem(title: L("状态加载中…", "Loading status…"), action: nil, keyEquivalent: "")
    private lazy var detailItem = NSMenuItem(title: "", action: nil, keyEquivalent: "")
    private lazy var limitPrimaryItem = NSMenuItem(title: L("5小时限额: 加载中…", "5h quota: loading…"), action: nil, keyEquivalent: "")
    private lazy var limitSecondaryItem = NSMenuItem(title: L("1周限额: 加载中…", "1w quota: loading…"), action: nil, keyEquivalent: "")
    private lazy var openSettingsItem = NSMenuItem(title: L("打开设置", "Open Settings"), action: #selector(openSettings), keyEquivalent: ",")
    private lazy var viewStatusItem = NSMenuItem(title: L("查看状态", "View Status"), action: #selector(showStatus), keyEquivalent: "i")
    private lazy var startItem = NSMenuItem(title: L("启动 Bridge", "Start Bridge"), action: #selector(startBridge), keyEquivalent: "s")
    private lazy var stopItem = NSMenuItem(title: L("停止 Bridge", "Stop Bridge"), action: #selector(stopBridge), keyEquivalent: "")
    private lazy var restartItem = NSMenuItem(title: L("重启 Bridge", "Restart Bridge"), action: #selector(restartBridge), keyEquivalent: "r")
    private lazy var autostartItem = NSMenuItem(title: L("开机启动 Bridge", "Launch Bridge at Login"), action: #selector(toggleAutostart), keyEquivalent: "")
    private lazy var openWorkspaceItem = NSMenuItem(title: L("打开项目目录", "Open Project Directory"), action: #selector(openWorkspaceFolder), keyEquivalent: "")
    private lazy var openRuntimeItem = NSMenuItem(title: L("打开数据目录", "Open Data Directory"), action: #selector(openRuntimeFolder), keyEquivalent: "")
    private lazy var openStdoutLogItem = NSMenuItem(title: L("打开标准日志", "Open Standard Log"), action: #selector(openStdoutLog), keyEquivalent: "")
    private lazy var openStderrLogItem = NSMenuItem(title: L("打开错误日志", "Open Error Log"), action: #selector(openStderrLog), keyEquivalent: "")
    private lazy var refreshItem = NSMenuItem(title: L("刷新状态", "Refresh Status"), action: #selector(refreshStatusAction), keyEquivalent: "")
    private lazy var quitItem = NSMenuItem(title: L("退出", "Quit"), action: #selector(quit), keyEquivalent: "q")

    private var currentStatus: BridgeStatus?
    private var currentLimits: CodexLimits?
    private var refreshTimer: Timer?
    private var lastLimitsRefreshAt: Date?
    private var limitsRefreshInFlight = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        if let appIcon = bundledAppIcon() {
            NSApp.applicationIconImage = appIcon
        }
        do {
            try configStore.prepareRuntime()
        } catch {
            showError(message: error.localizedDescription)
        }

        helper = BridgeHelper(projectRoot: configStore.runtimeRoot, helperPath: configStore.helperBinaryPath)
        setAppLanguagePreference(configStore.loadConfig()?.language ?? "auto")
        configureMenu()
        refreshStatus(showErrors: false)

        if configStore.needsSetup() {
            showSetupWindow()
        }

        refreshTimer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            self?.refreshStatus(showErrors: false)
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        refreshTimer?.invalidate()
    }

    private func configureMenu() {
        if let button = statusItem.button {
            button.title = L("Bridge", "Bridge")
        }

        summaryItem.isEnabled = false
        detailItem.isEnabled = false
        limitPrimaryItem.isEnabled = false
        limitSecondaryItem.isEnabled = false

        for item in [openSettingsItem, viewStatusItem, startItem, stopItem, restartItem, autostartItem, openWorkspaceItem, openRuntimeItem, openStdoutLogItem, openStderrLogItem, refreshItem, quitItem] {
            item.target = self
        }

        menu.addItem(summaryItem)
        menu.addItem(detailItem)
        menu.addItem(limitPrimaryItem)
        menu.addItem(limitSecondaryItem)
        menu.addItem(.separator())
        menu.addItem(openSettingsItem)
        menu.addItem(viewStatusItem)
        menu.addItem(startItem)
        menu.addItem(stopItem)
        menu.addItem(restartItem)
        menu.addItem(.separator())
        menu.addItem(autostartItem)
        menu.addItem(.separator())
        menu.addItem(openWorkspaceItem)
        menu.addItem(openRuntimeItem)
        menu.addItem(openStdoutLogItem)
        menu.addItem(openStderrLogItem)
        menu.addItem(.separator())
        menu.addItem(refreshItem)
        menu.addItem(quitItem)

        statusItem.menu = menu
    }

    private func reloadStaticMenuTitles() {
        openSettingsItem.title = L("打开设置", "Open Settings")
        viewStatusItem.title = L("查看状态", "View Status")
        startItem.title = L("启动 Bridge", "Start Bridge")
        stopItem.title = L("停止 Bridge", "Stop Bridge")
        restartItem.title = L("重启 Bridge", "Restart Bridge")
        autostartItem.title = L("开机启动 Bridge", "Launch Bridge at Login")
        openWorkspaceItem.title = L("打开项目目录", "Open Project Directory")
        openRuntimeItem.title = L("打开数据目录", "Open Data Directory")
        openStdoutLogItem.title = L("打开标准日志", "Open Standard Log")
        openStderrLogItem.title = L("打开错误日志", "Open Error Log")
        refreshItem.title = L("刷新状态", "Refresh Status")
        quitItem.title = L("退出", "Quit")
        applyLimits(currentLimits)
        if let status = currentStatus {
            applyStatus(status)
        } else if configStore.needsSetup() {
            summaryItem.title = L("需要初次配置", "Setup Required")
            detailItem.title = L("请打开设置完成 Bot 和 Codex 配置", "Open Settings to finish your Bot and Codex configuration")
        }
    }

    @objc private func refreshStatusAction() {
        refreshStatus(showErrors: true)
        refreshLimitsIfNeeded(force: true)
    }

    private func refreshStatus(showErrors: Bool) {
        if configStore.needsSetup() {
            summaryItem.title = L("需要初次配置", "Setup Required")
            detailItem.title = L("请打开设置完成 Bot 和 Codex 配置", "Open Settings to finish your Bot and Codex configuration")
            applyLimits(nil)
            updateStatusIcon(status: nil, error: false, codexReady: false)
            return
        }

        summaryItem.title = L("状态刷新中…", "Refreshing status…")
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self else { return }
            do {
                let status = try self.helper.status()
                DispatchQueue.main.async {
                    self.applyStatus(status)
                }
            } catch {
                DispatchQueue.main.async {
                    self.currentStatus = nil
                    self.summaryItem.title = L("Bridge 状态获取失败", "Failed to load Bridge status")
                    self.detailItem.title = error.localizedDescription
                    self.updateStatusIcon(status: nil, error: true, codexReady: false)
                    if showErrors {
                        self.showError(message: error.localizedDescription)
                    }
                }
            }
        }
    }

    private func applyStatus(_ status: BridgeStatus) {
        currentStatus = status

        if let codex = status.codex, !codex.ready {
            summaryItem.title = L("Codex 未就绪", "Codex Not Ready")
            detailItem.title = codex.error ?? codex.login_status ?? L("请先安装或登录 Codex", "Please install or sign in to Codex")
        } else if status.running {
            if let pid = status.pid {
                summaryItem.title = L("Bridge 运行中", "Bridge Running")
                detailItem.title = "launchd PID \(pid)"
            } else if let unmanaged = status.unmanaged_pid {
                summaryItem.title = L("Bridge 运行中", "Bridge Running")
                detailItem.title = L("手动启动 PID ", "Manual PID ") + "\(unmanaged)"
            } else {
                summaryItem.title = L("Bridge 运行中", "Bridge Running")
                detailItem.title = status.description ?? L("已启动", "Started")
            }
        } else if status.installed {
            summaryItem.title = L("Bridge 已安装但未运行", "Bridge Installed but Not Running")
            detailItem.title = status.description ?? L("可从菜单手动启动", "Start it manually from the menu")
        } else {
            summaryItem.title = L("Bridge 未安装", "Bridge Not Installed")
            detailItem.title = L("可用菜单启动或开启开机启动", "Start it from the menu or enable launch at login")
        }

        autostartItem.state = status.auto_start ? .on : .off
        stopItem.isEnabled = status.running && status.unmanaged_pid == nil
        startItem.isEnabled = !status.running
        restartItem.isEnabled = status.running || status.installed

        updateStatusIcon(status: status, error: false, codexReady: status.codex?.ready ?? false)
        refreshLimitsIfNeeded(force: false)
    }

    private func refreshLimitsIfNeeded(force: Bool) {
        guard !configStore.needsSetup() else {
            applyLimits(nil)
            return
        }

        guard currentStatus?.codex?.ready != false else {
            limitPrimaryItem.title = L("5小时限额: Codex 未就绪", "5h quota: Codex not ready")
            limitSecondaryItem.title = L("1周限额: Codex 未就绪", "1w quota: Codex not ready")
            return
        }

        if limitsRefreshInFlight {
            return
        }

        if !force, let lastLimitsRefreshAt, Date().timeIntervalSince(lastLimitsRefreshAt) < 60, currentLimits != nil {
            applyLimits(currentLimits)
            return
        }

        limitsRefreshInFlight = true
        DispatchQueue.global(qos: .utility).async { [weak self] in
            guard let self else { return }
            do {
                let limits = try self.helper.limits()
                DispatchQueue.main.async {
                    self.limitsRefreshInFlight = false
                    self.lastLimitsRefreshAt = Date()
                    self.currentLimits = limits
                    self.applyLimits(limits)
                }
            } catch {
                DispatchQueue.main.async {
                    self.limitsRefreshInFlight = false
                    if self.currentLimits == nil {
                        self.applyLimits(nil)
                    }
                }
            }
        }
    }

    private func applyLimits(_ limits: CodexLimits?) {
        limitPrimaryItem.title = formatLimitPrimaryLine(limits)
        limitSecondaryItem.title = formatLimitSecondaryLine(limits)
    }

    private func formatLimitLine(_ limits: CodexLimits?) -> String {
        return [formatLimitPrimaryLine(limits), formatLimitSecondaryLine(limits)].joined(separator: "\n")
    }

    private func formatLimitDetails(_ limits: CodexLimits?) -> String {
        guard let limits else {
            return L("暂不可用", "unavailable")
        }
        if !limits.available {
            return L("暂不可用", "unavailable")
        }

        var fragments: [String] = []
        if let primary = limits.primary_window {
            fragments.append(
                L("5小时剩余", "5h ") +
                "\(primary.remaining_percent)%" +
                L(" 重置 ", " left reset ") +
                formatLimitReset(primary.reset_at, short: true)
            )
        }
        if let secondary = limits.secondary_window {
            fragments.append(
                L("1周剩余", "1w ") +
                "\(secondary.remaining_percent)%" +
                L(" 重置 ", " left reset ") +
                formatLimitReset(secondary.reset_at, short: false)
            )
        }
        if fragments.isEmpty {
            return L("暂不可用", "unavailable")
        }
        return fragments.joined(separator: " · ")
    }

    private func formatLimitPrimaryLine(_ limits: CodexLimits?) -> String {
        guard let limits, limits.available, let primary = limits.primary_window else {
            return L("5小时限额: 暂不可用", "5h quota: unavailable")
        }
        return L("5小时限额: 剩余", "5h quota: ") + "\(primary.remaining_percent)%" + L(" 重置 ", " left, resets ") + formatLimitReset(primary.reset_at, short: true)
    }

    private func formatLimitSecondaryLine(_ limits: CodexLimits?) -> String {
        guard let limits, limits.available, let secondary = limits.secondary_window else {
            return L("1周限额: 暂不可用", "1w quota: unavailable")
        }
        return L("1周限额: 剩余", "1w quota: ") + "\(secondary.remaining_percent)%" + L(" 重置 ", " left, resets ") + formatLimitReset(secondary.reset_at, short: false)
    }

    private func formatLimitReset(_ unix: Int64, short: Bool) -> String {
        guard unix > 0 else { return "-" }
        let date = Date(timeIntervalSince1970: TimeInterval(unix))
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: resolvedAppLanguage() == .zh ? "zh_CN" : "en_US_POSIX")
        formatter.timeZone = .current
        formatter.dateFormat = short ? "HH:mm" : (resolvedAppLanguage() == .zh ? "M月d日" : "MMM d")
        return formatter.string(from: date)
    }

    private func updateStatusIcon(status: BridgeStatus?, error: Bool, codexReady: Bool) {
        guard let button = statusItem.button else { return }

        let symbolName: String
        switch (error, codexReady, status?.running, status?.unmanaged_pid != nil, status?.installed) {
        case (true, _, _, _, _):
            symbolName = "exclamationmark.triangle"
        case (_, false, _, _, _):
            symbolName = "xmark.octagon"
        case (_, true, true, false, _):
            symbolName = "paperplane.circle.fill"
        case (_, true, true, true, _):
            symbolName = "paperplane.circle"
        case (_, true, _, _, true):
            symbolName = "paperplane"
        default:
            symbolName = "paperplane"
        }

        button.image = NSImage(systemSymbolName: symbolName, accessibilityDescription: L("Telegram Codex Bridge", "Telegram Codex Bridge"))
        button.image?.isTemplate = true
        button.title = ""
    }

    @objc private func openSettings() {
        showSetupWindow()
    }

    @objc private func showStatus() {
        guard let status = currentStatus else {
            showError(message: L("当前还没有可展示的状态信息。", "There is no status information to show yet."))
            return
        }

        let configuredWorkspace = configStore.loadConfig()?.workspaceRoot.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let whisperSummary: String
        if let whisper = try? helper.whisperStatus() {
            if whisper.installed {
                let source = whisperSourceLabel(whisper.source)
                whisperSummary = L("已安装", "Installed") + (whisper.version.map { " (\($0))" } ?? "") + (source.isEmpty ? "" : " [\(source)]")
            } else {
                whisperSummary = whisper.error ?? L("未安装", "Not installed")
            }
        } else {
            whisperSummary = L("状态未知", "Unknown")
        }

        let lines = [
            "\(L("标签", "Label")): \(status.label)",
            "\(L("版本", "Version")): \(status.version ?? "-")",
            "\(L("运行中", "Running")): \(status.running ? L("是", "Yes") : L("否", "No"))",
            "\(L("由 launchd 管理", "Managed by launchd")): \(status.loaded ? L("是", "Yes") : L("否", "No"))",
            "\(L("开机启动", "Launch at login")): \(status.auto_start ? L("开", "On") : L("关", "Off"))",
            "\(L("描述", "Description")): \(status.description ?? "-")",
            "\(L("Codex 就绪", "Codex Ready")): \(status.codex?.ready == true ? L("是", "Yes") : L("否", "No"))",
            "\(L("Codex 状态", "Codex Status")): \(status.codex?.login_status ?? status.codex?.error ?? "-")",
            "\(L("Codex 权限", "Codex Permission")): \(permissionModeLabel(status.permission_mode ?? "default"))",
            "\(L("Whisper 语音转写", "Whisper Speech-to-Text")): \(whisperSummary)",
            "\(L("当前限额", "Current Quota")): \(formatLimitDetails(currentLimits))",
            "\(L("项目目录", "Project Directory")): \(configuredWorkspace.isEmpty ? "-" : configuredWorkspace)",
            "\(L("程序数据目录", "App Data Directory")): \(status.paths.project_root)",
            "\(L("标准日志", "Standard Log")): \(status.paths.stdout_log)",
            "\(L("错误日志", "Error Log")): \(status.paths.stderr_log)",
        ]

        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "Telegram Codex Bridge"
        alert.informativeText = lines.joined(separator: "\n")
        alert.icon = bundledAppIcon()
        alert.addButton(withTitle: L("好的", "OK"))
        alert.runModal()
    }

    @objc private func startBridge() {
        runAction(L("启动 Bridge", "Start Bridge")) { try self.helper.start() }
    }

    @objc private func stopBridge() {
        runAction(L("停止 Bridge", "Stop Bridge")) { try self.helper.stop() }
    }

    @objc private func restartBridge() {
        runAction(L("重启 Bridge", "Restart Bridge")) { try self.helper.restart() }
    }

    @objc private func toggleAutostart() {
        let enabled = !(currentStatus?.auto_start ?? false)
        runAction(enabled ? L("开启开机启动", "Enable Launch at Login") : L("关闭开机启动", "Disable Launch at Login")) {
            try self.helper.setAutostart(enabled)
        }
    }

    @objc private func openRuntimeFolder() {
        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: configStore.runtimeRoot)
    }

    @objc private func openWorkspaceFolder() {
        guard let workspace = configStore.loadConfig()?.workspaceRoot.trimmingCharacters(in: .whitespacesAndNewlines), !workspace.isEmpty else {
            showError(message: L("当前还没有配置项目目录。", "No project directory is configured yet."))
            return
        }

        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: workspace)
    }

    @objc private func openStdoutLog() {
        openFile(currentStatus?.paths.stdout_log ?? (configStore.runtimeRoot + "/data/logs/bridge.stdout.log"))
    }

    @objc private func openStderrLog() {
        openFile(currentStatus?.paths.stderr_log ?? (configStore.runtimeRoot + "/data/logs/bridge.stderr.log"))
    }

    @objc private func quit() {
        NSApp.terminate(nil)
    }

    private func showSetupWindow() {
        if let existing = configStore.loadConfig() {
            setAppLanguagePreference(existing.language)
            reloadStaticMenuTitles()
        }

        if setupWindowController == nil {
            let controller = SetupWindowController(
                existingConfig: configStore.loadConfig(),
                codexProvider: { try self.helper.codexStatus() },
                whisperProvider: { try self.helper.whisperStatus() },
                installWhisperHandler: { try await Task.detached(priority: .userInitiated) { try self.helper.installWhisper() }.value },
                saveHandler: { config in
                    try self.helper.validateTelegramToken(config.botToken)
                    var finalConfig = config
                    if finalConfig.codexBinary.isEmpty {
                        finalConfig.codexBinary = try self.helper.codexStatus().resolved_binary ?? "codex"
                    }

                    let workspace = finalConfig.workspaceRoot.trimmingCharacters(in: .whitespacesAndNewlines)
                    var isDirectory: ObjCBool = false
                    guard FileManager.default.fileExists(atPath: workspace, isDirectory: &isDirectory), isDirectory.boolValue else {
                        throw NSError(domain: "BridgeStatusBarApp", code: 1, userInfo: [NSLocalizedDescriptionKey: L("项目目录不存在", "Project directory does not exist")])
                    }

                    try self.configStore.saveConfig(finalConfig)
                    setAppLanguagePreference(finalConfig.language)
                    try self.helper.setAutostart(finalConfig.autoStart)
                    let status = try self.helper.status()
                    if status.unmanaged_pid != nil {
                        try self.helper.stopUnmanaged()
                        try await Task.sleep(nanoseconds: 800_000_000)
                    }
                    try self.helper.start()
                    await MainActor.run {
                        self.setupWindowController = nil
                        self.reloadStaticMenuTitles()
                        self.refreshStatus(showErrors: false)
                    }
                }
            )
            setupWindowController = controller
        }

        if let existing = configStore.loadConfig() {
            setupWindowController?.updateConfig(existing)
        }

        setupWindowController?.refreshDiagnostics()
        setupWindowController?.showWindow(nil)
        setupWindowController?.window?.center()
        NSApp.activate(ignoringOtherApps: true)
    }

    private func openFile(_ path: String) {
        let url = URL(fileURLWithPath: path)
        if FileManager.default.fileExists(atPath: path) {
            NSWorkspace.shared.open(url)
        } else {
            showError(message: L("文件还不存在：", "File does not exist yet: ") + path)
        }
    }

    private func runAction(_ title: String, action: @escaping () throws -> Void) {
        summaryItem.title = resolvedAppLanguage() == .zh ? "\(title)中…" : "\(title)…"
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self else { return }
            do {
                try action()
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
                    self.refreshStatus(showErrors: true)
                }
            } catch {
                DispatchQueue.main.async {
                    self.refreshStatus(showErrors: false)
                    self.showError(message: error.localizedDescription)
                }
            }
        }
    }

    private func showError(message: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = L("操作失败", "Action Failed")
        alert.informativeText = message
        alert.icon = bundledAppIcon()
        alert.addButton(withTitle: L("好的", "OK"))
        alert.runModal()
    }

    private func permissionModeLabel(_ raw: String) -> String {
        switch raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "full", "full-access", "danger", "danger-full-access":
            return L("完全访问", "Full Access")
        default:
            return L("默认", "Default")
        }
    }

}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
