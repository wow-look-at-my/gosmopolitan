// Prints each text in Activity Monitor's window that holds one of the words
// given as arguments, one per line. The table exposes only the rows on the
// screen, so the reader scrolls it from top to bottom and reads each step.

import AppKit
import ApplicationServices

func attribute(_ elem: AXUIElement, _ name: String) -> CFTypeRef? {
	var value: CFTypeRef?
	guard AXUIElementCopyAttributeValue(elem, name as CFString, &value) == .success else {
		return nil
	}
	return value
}

func collect(_ elem: AXUIElement, _ depth: Int, _ texts: inout Set<String>, _ bars: inout [AXUIElement]) {
	if depth > 40 {
		return
	}
	if attribute(elem, kAXRoleAttribute) as? String == kAXScrollBarRole,
		attribute(elem, kAXOrientationAttribute) as? String == kAXVerticalOrientationValue {
		bars.append(elem)
	}
	for name in [kAXValueAttribute, kAXTitleAttribute, kAXDescriptionAttribute] {
		if let text = attribute(elem, name) as? String, !text.isEmpty {
			texts.insert(text)
		}
	}
	for child in attribute(elem, kAXChildrenAttribute) as? [AXUIElement] ?? [] {
		collect(child, depth + 1, &texts, &bars)
	}
}

let words = Array(CommandLine.arguments.dropFirst())
if words.isEmpty {
	print("usage: activitymonitor WORD...")
	exit(2)
}
if !AXIsProcessTrusted() {
	print("this process may not use the accessibility interface")
	exit(2)
}
guard let app = NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.ActivityMonitor").first else {
	print("Activity Monitor is not running")
	exit(2)
}
let root = AXUIElementCreateApplication(app.processIdentifier)
AXUIElementSetMessagingTimeout(root, 5)

// Activity Monitor samples every few seconds, so a new process shows up late.
var seen = Set<String>()
let deadline = Date().addingTimeInterval(90)
while Date() < deadline {
	var bars: [AXUIElement] = []
	collect(root, 0, &seen, &bars)
	for step in 0...20 {
		for bar in bars {
			AXUIElementSetAttributeValue(bar, kAXValueAttribute as CFString, NSNumber(value: Double(step) / 20))
		}
		Thread.sleep(forTimeInterval: 0.2)
		var unused: [AXUIElement] = []
		collect(root, 0, &seen, &unused)
	}
	if seen.contains(where: { $0.contains(words[0]) }) {
		break
	}
	Thread.sleep(forTimeInterval: 2)
}

let hits = seen.filter { text in words.contains { text.contains($0) } }.sorted()
if hits.isEmpty {
	print("no text holds \(words.joined(separator: " or ")); \(seen.count) texts, such as: \(seen.sorted().prefix(80).joined(separator: " | "))")
	exit(1)
}
for hit in hits {
	print(hit)
}
