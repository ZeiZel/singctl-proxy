// SegmentedControl.swift
//
// A generic segmented picker (used by the Dashboard's off/proxy/vpn mode
// switch, but works for any `Hashable` value) backed by an NSSegmentedControl.

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

/// Keep the same native control and first responder across polling and mode changes.
struct SegmentedControl<T: Hashable>: NSViewRepresentable {
    let options: [SegmentedOption<T>]
    @Binding var selection: T
    var disabled: Bool = false

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSSegmentedControl {
        let control = NSSegmentedControl(labels: options.map(\.label), trackingMode: .selectOne,
                                         target: context.coordinator, action: #selector(Coordinator.choose(_:)))
        control.controlSize = .large
        control.segmentDistribution = .fillEqually
        control.setContentHuggingPriority(.defaultLow, for: .horizontal)
        return control
    }

    func updateNSView(_ control: NSSegmentedControl, context: Context) {
        context.coordinator.picker = self
        if control.segmentCount != options.count { control.segmentCount = options.count }
        let selectedValue = selection
        for (index, option) in options.enumerated() {
            if control.label(forSegment: index) != option.label {
                control.setLabel(option.label, forSegment: index)
            }
            // Disabling the focused NSSegmentedControl during a layout update
            // makes AppKit search for the next key view and re-enter SwiftUI's
            // focus graph on macOS 26.2. Only lock the alternative segments.
            let enabled = context.environment.isEnabled && (!disabled || option.value == selectedValue)
            if control.isEnabled(forSegment: index) != enabled {
                control.setEnabled(enabled, forSegment: index)
            }
        }
        let index = options.firstIndex { $0.value == selectedValue } ?? -1
        if control.selectedSegment != index { control.selectedSegment = index }
    }

    @MainActor
    final class Coordinator: NSObject {
        var picker: SegmentedControl
        init(_ picker: SegmentedControl) { self.picker = picker }

        @objc func choose(_ control: NSSegmentedControl) {
            let index = control.selectedSegment
            if !picker.disabled, picker.options.indices.contains(index), control.isEnabled(forSegment: index) {
                let requested = picker.options[index].value
                if requested != picker.selection { picker.selection = requested }
            }
            // A rejected or cancelled action must leave the bound value selected.
            control.selectedSegment = picker.options.firstIndex { $0.value == picker.selection } ?? -1
        }
    }
}
