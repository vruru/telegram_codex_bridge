import Foundation

/// Foundation-only .env merger compatible with the Go parser semantics.
/// Go side: TrimSpace(full line) → SplitN '=' 2 → TrimSpace(key), TrimSpace(value), no quote stripping.
struct EnvFileMerger {

    // MARK: - UI keys managed by the macOS App

    static let uiKeys: Set<String> = [
        "TELEGRAM_BOT_TOKEN",
        "TELEGRAM_ALLOWED_USER_IDS",
        "TELEGRAM_ALLOWED_CHAT_IDS",
        "CODEX_WORKSPACE_ROOT",
        "CODEX_BIN",
        "BRIDGE_LANGUAGE",
        "CODEX_PERMISSION_MODE",
    ]

    // MARK: - Public API

    /// Merge `uiValues` (key → value) into the .env file at `path`.
    /// - Non-UI lines, comments, blanks are preserved verbatim (in original order).
    /// - All occurrences of a UI key are replaced so duplicate old values cannot win.
    /// - Missing UI keys are appended at the end.
    /// - Throws on read error (file-not-exist is treated as empty) and on injection.
    /// - Output is atomically written with 0600 permissions.
    static func merge(uiValues: [String: String], at path: String) throws {
        // 1. Validate values (no newline / carriage-return injection)
        for (key, value) in uiValues {
            if value.contains("\n") || value.contains("\r") {
                throw EnvFileError.injection(key: key)
            }
        }

        // 2. Read existing content
        var lines: [String] = []
        do {
            let data = try Data(contentsOf: URL(fileURLWithPath: path))
            guard let text = String(data: data, encoding: .utf8) else {
                throw EnvFileError.readFailure(path: path, reason: "not valid UTF-8")
            }
            // Split on newline without changing untouched line terminators.
            let raw = text.components(separatedBy: "\n")
            // Remove final empty element from trailing newline
            if let last = raw.last, last.isEmpty {
                lines = Array(raw.dropLast())
            } else {
                lines = raw
            }
            // Keep CRLF terminators on untouched lines. Parsing trims the CR.
        } catch let e as EnvFileError {
            throw e
        } catch {
            // File-not-exist → treat as empty
            let ns = error as NSError
            if ns.domain == NSCocoaErrorDomain && ns.code == NSFileReadNoSuchFileError {
                lines = []
            } else {
                throw EnvFileError.readFailure(path: path, reason: error.localizedDescription)
            }
        }

        // 3. Identify UI key indices and build output
        var outputLines: [String] = []
        var seenKeys = Set<String>()

        for line in lines {
            let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
            // Preserve non-key lines verbatim
            if trimmed.isEmpty || trimmed.hasPrefix("#") || !trimmed.contains("=") {
                outputLines.append(line)
                continue
            }
            // Parse key the same way Go does
            guard let eqIdx = trimmed.firstIndex(of: "=") else {
                outputLines.append(line)
                continue
            }
            let rawKey = String(trimmed[trimmed.startIndex..<eqIdx])
            let parsedKey = rawKey.trimmingCharacters(in: .whitespaces)

            if uiKeys.contains(parsedKey) {
                let value = uiValues[parsedKey] ?? ""
                outputLines.append("\(parsedKey)=\(value)")
                seenKeys.insert(parsedKey)
            } else {
                // Non-UI key=value line: preserve verbatim
                outputLines.append(line)
            }
        }

        // 4. Append missing UI keys (stable order)
        for key in uiKeys.sorted() {
            if !seenKeys.contains(key) {
                outputLines.append("\(key)=\(uiValues[key] ?? "")")
            }
        }

        // 5. Atomic write with 0600
        let content = outputLines.joined(separator: "\n") + "\n"
        try content.write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes(
            [.posixPermissions: 0o600],
            ofItemAtPath: path
        )
    }

    // MARK: - Errors

    enum EnvFileError: Error, CustomStringConvertible {
        case injection(key: String)
        case readFailure(path: String, reason: String)

        var description: String {
            switch self {
            case .injection(let k):
                return "EnvFile: illegal newline in value for \(k)"
            case .readFailure(let p, let r):
                return "EnvFile: read failure at \(p): \(r)"
            }
        }
    }
}
