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
    /// Changes whenever an action rejected a picker selection.  This is part
    /// of the equatable render identity so the native control is resynchronised
    /// with the source of truth after a cancelled confirmation or failed write.
    var resyncToken: Int = 0

    /// Callers use this when their binding setter rejects a native picker
    /// click (for example, a cancelled VPN confirmation). Keeping the token
    /// transition here gives tests and every picker the same resync rule.
    static func nextResyncToken(after token: Int) -> Int { token &+ 1 }

    #if SCREENSHOT_HARNESS
    static func snapshotsEqual(
        oldOptions: [SegmentedOption<T>], oldSelection: T, oldDisabled: Bool, oldResyncToken: Int,
        newOptions: [SegmentedOption<T>], newSelection: T, newDisabled: Bool, newResyncToken: Int
    ) -> Bool {
        EquatableSegmentedPicker.snapshotsEqual(
            oldOptions: oldOptions, oldSelection: oldSelection, oldDisabled: oldDisabled,
            oldResyncToken: oldResyncToken, newOptions: newOptions, newSelection: newSelection,
            newDisabled: newDisabled, newResyncToken: newResyncToken
        )
    }
    #endif

    /// - Parameters:
    ///   - options: Choices in display order.
    ///   - selection: Bound current value; tapping a segment sets it.
    ///   - disabled: Disables all segments (e.g. while a change is in-flight).
    init(options: [SegmentedOption<T>], selection: Binding<T>, disabled: Bool = false, resyncToken: Int = 0) {
        self.options = options
        self._selection = selection
        self.disabled = disabled
        self.resyncToken = resyncToken
    }

    var body: some View {
        EquatableSegmentedPicker(
            options: options,
            selectionValue: selection,
            selection: $selection,
            disabled: disabled,
            resyncToken: resyncToken
        )
        .equatable()
    }
}

/// Keeps the native picker behind one stable Equatable view.  SwiftUI's
/// macOS 26 segmented bridge otherwise accumulates an ObservationRegistrar
/// when a fresh binding is supplied on every parent update.
fileprivate struct EquatableSegmentedPicker<T: Hashable>: View, Equatable {
    let options: [SegmentedOption<T>]
    /// Snapshot captured by the parent render. Equality must never read the
    /// live Binding, or both old and new views can observe the same new value.
    let selectionValue: T
    @Binding var selection: T
    let disabled: Bool
    let resyncToken: Int

    static func == (lhs: Self, rhs: Self) -> Bool {
        snapshotsEqual(
            oldOptions: lhs.options,
            oldSelection: lhs.selectionValue,
            oldDisabled: lhs.disabled,
            oldResyncToken: lhs.resyncToken,
            newOptions: rhs.options,
            newSelection: rhs.selectionValue,
            newDisabled: rhs.disabled,
            newResyncToken: rhs.resyncToken
        )
    }

    /// Equality intentionally consumes frozen render snapshots. The Binding
    /// remains available to the Picker for events, but is never read while
    /// deciding whether an old and new render are equal.
    static func snapshotsEqual(
        oldOptions: [SegmentedOption<T>], oldSelection: T, oldDisabled: Bool, oldResyncToken: Int,
        newOptions: [SegmentedOption<T>], newSelection: T, newDisabled: Bool, newResyncToken: Int
    ) -> Bool {
        guard oldOptions.count == newOptions.count,
              oldSelection == newSelection,
              oldDisabled == newDisabled,
              oldResyncToken == newResyncToken else { return false }
        return zip(oldOptions, newOptions).allSatisfy { left, right in
            left.value == right.value && left.label == right.label
        }
    }

    var body: some View {
        Picker("", selection: $selection) {
            ForEach(options) { option in
                Text(option.label).tag(option.value)
            }
        }
        .pickerStyle(.segmented)
        .controlSize(.large)
        .labelsHidden()
        .disabled(disabled)
    }
}
