// SegmentedControl.swift
//
// A generic pill-style segmented picker (used by the Dashboard's off/proxy/vpn
// mode switch, but works for any `Hashable` value).

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
        HStack(spacing: 2) {
            ForEach(options) { option in
                let active = option.value == selection
                Text(option.label)
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(active ? Color.sText : Color.sTextDim)
                    .padding(.horizontal, Spacing.md)
                    .padding(.vertical, 6)
                    .background(active ? Color.sAccent.opacity(0.22) : Color.clear)
                    .clipShape(RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
                    .contentShape(Rectangle())
                    .onTapGesture {
                        guard !disabled else { return }
                        selection = option.value
                    }
            }
        }
        .padding(2)
        .background(Color.sBgSoft)
        .clipShape(RoundedRectangle(cornerRadius: Radius.md, style: .continuous))
        .opacity(disabled ? 0.6 : 1)
        .allowsHitTesting(!disabled)
    }
}
