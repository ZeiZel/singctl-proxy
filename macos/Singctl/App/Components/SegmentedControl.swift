// SegmentedControl.swift
//
// A generic segmented picker (used by the Dashboard's off/proxy/vpn mode
// switch, but works for any `Hashable` value). Wraps a native
// `Picker(...).pickerStyle(.segmented)` so it renders as a standard macOS
// segmented control.

import SwiftUI

/// One choice in a `SegmentedControl`.
struct SegmentedOption<T: Hashable>: Identifiable {
    let value: T
    let label: String
    var id: T { value }

    init(_ value: T, _ label: String) {
        self.value = value
        self.label = label
    }
}

/// ```swift
/// SegmentedControl(
///     options: [.init("off", "Off"), .init("proxy", "Proxy"), .init("vpn", "VPN")],
///     selection: $mode
/// )
/// SegmentedControl(options: modeOptions, selection: $mode, disabled: isApplying)
/// ```
struct SegmentedControl<T: Hashable>: View {
    let options: [SegmentedOption<T>]
    @Binding var selection: T
    var disabled: Bool = false

    /// - Parameters:
    ///   - options: Choices in display order.
    ///   - selection: Bound current value; tapping a segment sets it.
    ///   - disabled: Disables all segments (e.g. while a change is in-flight).
    init(options: [SegmentedOption<T>], selection: Binding<T>, disabled: Bool = false) {
        self.options = options
        self._selection = selection
        self.disabled = disabled
    }

    var body: some View {
        Picker("", selection: $selection) {
            ForEach(options) { option in
                Text(option.label).tag(option.value)
            }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .disabled(disabled)
    }
}
