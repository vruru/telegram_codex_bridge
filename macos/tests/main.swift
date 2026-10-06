import Foundation

// MARK: - Minimal test harness (no XCTest)

var failures = 0
var passes = 0

func expect(_ condition: Bool, _ label: String) {
    if condition { passes += 1 }
    else { failures += 1; print("FAIL: \(label)") }
}

func writeFile(_ content: String, to path: String) throws {
    try content.write(toFile: path, atomically: true, encoding: .utf8)
}

func readFile(_ path: String) throws -> String {
    let data = try Data(contentsOf: URL(fileURLWithPath: path))
    return String(data: data, encoding: .utf8) ?? ""
}

// MARK: - Test scenarios

let tmp = NSTemporaryDirectory() + "EnvFileTests_\(UUID().uuidString)/"
try! FileManager.default.createDirectory(atPath: tmp, withIntermediateDirectories: true)
let env = tmp + ".env"

// --- 1. Non-existent file → treated as empty, all UI keys appended ---
do {
    try? FileManager.default.removeItem(atPath: env)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "t1",
            "TELEGRAM_ALLOWED_USER_IDS": "100",
            "TELEGRAM_ALLOWED_CHAT_IDS": "200",
            "CODEX_WORKSPACE_ROOT": "/tmp/ws",
            "CODEX_BIN": "codex",
            "BRIDGE_LANGUAGE": "zh",
            "CODEX_PERMISSION_MODE": "default",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.contains("TELEGRAM_BOT_TOKEN=t1"), "missing-file/appends-all")
    expect(content.hasSuffix("\n"), "missing-file/trailing-newline")
    // Check 0600
    let attrs = try FileManager.default.attributesOfItem(atPath: env)
    let perms = attrs[.posixPermissions] as! NSNumber
    expect(perms.intValue == 0o600, "missing-file/0600-perms")
}

// --- 2. Preserve non-UI keys, comments, blanks, order ---
do {
    let original = """
    # Telegram bot
    TELEGRAM_BOT_TOKEN=old
    BRIDGE_LOG_LEVEL=debug
    CODEX_PROVIDER=gemini

    # Custom
    MY_CUSTOM_KEY=hello
    BRIDGE_LOG_MAX_SIZE_MB=50
    """
    try writeFile(original, to: env)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "new_token",
            "TELEGRAM_ALLOWED_USER_IDS": "u1,u2",
            "TELEGRAM_ALLOWED_CHAT_IDS": "c1",
            "CODEX_WORKSPACE_ROOT": "/ws",
            "CODEX_BIN": "gemini",
            "BRIDGE_LANGUAGE": "en",
            "CODEX_PERMISSION_MODE": "full-access",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.contains("BRIDGE_LOG_LEVEL=debug"), "preserve/advanced-log")
    expect(content.contains("CODEX_PROVIDER=gemini"), "preserve/advanced-provider")
    expect(content.contains("MY_CUSTOM_KEY=hello"), "preserve/custom-unknown-key")
    expect(content.contains("# Telegram bot"), "preserve/comment-line")
    expect(content.contains("# Custom"), "preserve/comment-line-2")
    expect(content.contains("TELEGRAM_BOT_TOKEN=new_token"), "preserve/ui-updated")
    expect(content.contains("BRIDGE_LOG_MAX_SIZE_MB=50"), "preserve/log-max")
    expect(content.contains("TELEGRAM_ALLOWED_USER_IDS=u1,u2"), "missing-ui-keys-appended")
}

// --- 3. Duplicate UI keys: ALL occurrences updated ---
do {
    let original = """
    TELEGRAM_BOT_TOKEN=first
    CODEX_BIN=a
    TELEGRAM_BOT_TOKEN=second
    TELEGRAM_BOT_TOKEN=third
    """
    try writeFile(original, to: env)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "updated",
            "TELEGRAM_ALLOWED_USER_IDS": "",
            "TELEGRAM_ALLOWED_CHAT_IDS": "",
            "CODEX_WORKSPACE_ROOT": "",
            "CODEX_BIN": "newbin",
            "BRIDGE_LANGUAGE": "auto",
            "CODEX_PERMISSION_MODE": "default",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.components(separatedBy: "\n").filter({ $0.hasPrefix("TELEGRAM_BOT_TOKEN=") && $0 == "TELEGRAM_BOT_TOKEN=updated" }).count == 3,
           "dup-key/all-updated")
    expect(content.contains("CODEX_BIN=newbin"), "dup-key/second-ui-key")
}

// --- 4. Empty file ---
do {
    try writeFile("", to: env)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "x",
            "TELEGRAM_ALLOWED_USER_IDS": "",
            "TELEGRAM_ALLOWED_CHAT_IDS": "",
            "CODEX_WORKSPACE_ROOT": "",
            "CODEX_BIN": "",
            "BRIDGE_LANGUAGE": "",
            "CODEX_PERMISSION_MODE": "",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.contains("TELEGRAM_BOT_TOKEN=x"), "empty-file/appends-ui")
}

// --- 5. CRLF input ---
do {
    let crlf = "CODEX_PROVIDER=gemini\r\nTELEGRAM_BOT_TOKEN=old\r\n"
    try crlf.write(toFile: env, atomically: true, encoding: .utf8)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "crlf_token",
            "TELEGRAM_ALLOWED_USER_IDS": "",
            "TELEGRAM_ALLOWED_CHAT_IDS": "",
            "CODEX_WORKSPACE_ROOT": "",
            "CODEX_BIN": "",
            "BRIDGE_LANGUAGE": "",
            "CODEX_PERMISSION_MODE": "",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.contains("CODEX_PROVIDER=gemini\r\n"), "crlf/non-ui-bytes-preserved")
    expect(content.contains("TELEGRAM_BOT_TOKEN=crlf_token"), "crlf/ui-updated")
    expect(content.contains("CODEX_PROVIDER=gemini"), "crlf/non-ui-preserved")
}

// --- 6. Unicode values & keys ---
do {
    let original = "GEMINI_MODELS=gemini-2.5-flash\ncustom_中文=日本語\n"
    try writeFile(original, to: env)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "🤖token",
            "TELEGRAM_ALLOWED_USER_IDS": "",
            "TELEGRAM_ALLOWED_CHAT_IDS": "",
            "CODEX_WORKSPACE_ROOT": "/Users/用户/workspace",
            "CODEX_BIN": "",
            "BRIDGE_LANGUAGE": "",
            "CODEX_PERMISSION_MODE": "",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.contains("custom_中文=日本語"), "unicode/non-ui-preserved")
    expect(content.contains("TELEGRAM_BOT_TOKEN=🤖token"), "unicode/ui-value")
    expect(content.contains("CODEX_WORKSPACE_ROOT=/Users/用户/workspace"), "unicode/workspace-path")
}

// --- 7. Value containing '=' ---
do {
    let original = "CODEX_PERMISSION_MODE=default\ntoken=a=b=c\n"
    try writeFile(original, to: env)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "x=y=z",
            "TELEGRAM_ALLOWED_USER_IDS": "",
            "TELEGRAM_ALLOWED_CHAT_IDS": "",
            "CODEX_WORKSPACE_ROOT": "",
            "CODEX_BIN": "",
            "BRIDGE_LANGUAGE": "",
            "CODEX_PERMISSION_MODE": "",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.contains("token=a=b=c"), "eq-in-value/preserved-non-ui")
    expect(content.contains("TELEGRAM_BOT_TOKEN=x=y=z"), "eq-in-value/ui-update")
}

// --- 8. No trailing newline in source ---
do {
    let original = "CODEX_PROVIDER=cli\nTELEGRAM_BOT_TOKEN=nl_test"
    try writeFile(original, to: env)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "nl_ok",
            "TELEGRAM_ALLOWED_USER_IDS": "",
            "TELEGRAM_ALLOWED_CHAT_IDS": "",
            "CODEX_WORKSPACE_ROOT": "",
            "CODEX_BIN": "",
            "BRIDGE_LANGUAGE": "",
            "CODEX_PERMISSION_MODE": "",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.hasSuffix("\n"), "no-trailing-newline/fixed")
    expect(content.contains("CODEX_PROVIDER=cli"), "no-trailing-newline/preserved")
}

// --- 9. Injection: value with newline → throw ---
do {
    let original = "TELEGRAM_BOT_TOKEN=safe\n"
    try writeFile(original, to: env)
    var threw = false
    do {
        try EnvFileMerger.merge(
            uiValues: [
                "TELEGRAM_BOT_TOKEN": "evil\nCODEX_PROVIDER=hacked",
                "TELEGRAM_ALLOWED_USER_IDS": "",
                "TELEGRAM_ALLOWED_CHAT_IDS": "",
                "CODEX_WORKSPACE_ROOT": "",
                "CODEX_BIN": "",
                "BRIDGE_LANGUAGE": "",
                "CODEX_PERMISSION_MODE": "",
            ],
            at: env
        )
    } catch {
        threw = true
    }
    expect(threw, "injection/newline-throws")
}

// --- 10. Injection: value with \r → throw ---
do {
    var threw = false
    do {
        try EnvFileMerger.merge(
            uiValues: [
                "TELEGRAM_BOT_TOKEN": "cr\rinjection",
                "TELEGRAM_ALLOWED_USER_IDS": "",
                "TELEGRAM_ALLOWED_CHAT_IDS": "",
                "CODEX_WORKSPACE_ROOT": "",
                "CODEX_BIN": "",
                "BRIDGE_LANGUAGE": "",
                "CODEX_PERMISSION_MODE": "",
            ],
            at: env
        )
    } catch {
        threw = true
    }
    expect(threw, "injection/cr-throws")
}

// --- 11. Read failure on directory (not a file) → throw ---
do {
    let dirPath = tmp + "not_a_file_dir"
    try? FileManager.default.createDirectory(atPath: dirPath, withIntermediateDirectories: true)
    var threw = false
    do {
        try EnvFileMerger.merge(
            uiValues: [
                "TELEGRAM_BOT_TOKEN": "x",
                "TELEGRAM_ALLOWED_USER_IDS": "",
                "TELEGRAM_ALLOWED_CHAT_IDS": "",
                "CODEX_WORKSPACE_ROOT": "",
                "CODEX_BIN": "",
                "BRIDGE_LANGUAGE": "",
                "CODEX_PERMISSION_MODE": "",
            ],
            at: dirPath
        )
    } catch {
        threw = true
    }
    expect(threw, "read-error/directory-throws")
}

// --- 12. Leading/trailing spaces around key=value (Go TrimSpace compat) ---
do {
    let original = "  TELEGRAM_BOT_TOKEN = spaced_value  \nCODEX_BIN = codex \n"
    try writeFile(original, to: env)
    try EnvFileMerger.merge(
        uiValues: [
            "TELEGRAM_BOT_TOKEN": "new_val",
            "TELEGRAM_ALLOWED_USER_IDS": "",
            "TELEGRAM_ALLOWED_CHAT_IDS": "",
            "CODEX_WORKSPACE_ROOT": "",
            "CODEX_BIN": "new_bin",
            "BRIDGE_LANGUAGE": "",
            "CODEX_PERMISSION_MODE": "",
        ],
        at: env
    )
    let content = try readFile(env)
    expect(content.contains("TELEGRAM_BOT_TOKEN=new_val"), "spaces/key-value-trimmed-update")
    expect(content.contains("CODEX_BIN=new_bin"), "spaces/second-ui-key")
}

// MARK: - Cleanup & Report
_ = try? FileManager.default.removeItem(atPath: tmp)

print("\n\(passes) passed, \(failures) failed")
if failures > 0 {
    exit(1)
}
print("ALL ENVFILE TESTS PASSED")
