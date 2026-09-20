import Foundation

@main
struct SettingsDraftChecks {
    static func main() {
        let baseline = Settings(socksPort: 1080, clashEnabled: false, clashAddr: "127.0.0.1:9090",
                                urlTestURL: "https://example.test", urlTestInterval: "3m",
                                urlTestTolerance: 50, saveProfile: false, autostartMode: nil)
        let normalized = SettingsDraft.normalized(baseline)
        precondition(normalized.autostartMode == "off")
        precondition(!SettingsDraft.isDirty(draft: normalized, applied: baseline))
        precondition(SettingsDraft.validPort("1") && SettingsDraft.validPort("65535"))
        precondition(!SettingsDraft.validPort("0") && !SettingsDraft.validPort("65536") && !SettingsDraft.validPort("nope"))
        precondition(SettingsDraft.validTolerance("0") && SettingsDraft.validTolerance("50"))
        precondition(!SettingsDraft.validTolerance("-1") && !SettingsDraft.validTolerance("bad"))
        precondition(SettingsDraft.hasPortConflict(socksPort: "9090", clashEnabled: true, clashAddress: "127.0.0.1:9090"))
        precondition(!SettingsDraft.hasPortConflict(socksPort: "1080", clashEnabled: true, clashAddress: "127.0.0.1:9090"))
        var changed = normalized
        changed.socksPort = 1081
        precondition(SettingsDraft.isDirty(draft: changed, applied: normalized))
        changed = normalized
        precondition(!SettingsDraft.isDirty(draft: changed, applied: normalized))
        print("SettingsDraft checks passed")
    }
}
