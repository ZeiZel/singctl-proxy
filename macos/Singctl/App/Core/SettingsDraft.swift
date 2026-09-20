import Foundation

/// Pure snapshot and validation rules used by SettingsScreen.
enum SettingsDraft {
    static func normalized(_ settings: Settings) -> Settings {
        var result = settings
        result.autostartMode = settings.autostartMode ?? "off"
        return result
    }

    static func isDirty(draft: Settings, applied: Settings) -> Bool {
        normalized(draft) != normalized(applied)
    }

    static func validPort(_ text: String) -> Bool {
        guard let value = Int(text.trimmingCharacters(in: .whitespaces)) else { return false }
        return (1...65535).contains(value)
    }

    static func validTolerance(_ text: String) -> Bool {
        guard let value = Int(text.trimmingCharacters(in: .whitespaces)) else { return false }
        return value >= 0
    }

    static func hasPortConflict(socksPort: String, clashEnabled: Bool, clashAddress: String) -> Bool {
        guard let socks = Int(socksPort.trimmingCharacters(in: .whitespaces)), clashEnabled,
              let part = clashAddress.split(separator: ":").last,
              let clash = Int(part) else { return false }
        return socks == clash
    }
}
