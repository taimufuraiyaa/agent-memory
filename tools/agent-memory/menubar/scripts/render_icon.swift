import AppKit

let args = CommandLine.arguments
guard args.count >= 2 else {
    fputs("usage: swift render_icon.swift <output.png>\n", stderr)
    exit(1)
}

let output = URL(fileURLWithPath: args[1])
let size = NSSize(width: 1024, height: 1024)
let image = NSImage(size: size)

image.lockFocus()

let rect = NSRect(origin: .zero, size: size)
let bg = NSBezierPath(roundedRect: rect.insetBy(dx: 36, dy: 36), xRadius: 220, yRadius: 220)
let gradient = NSGradient(colors: [
    NSColor(calibratedRed: 0.13, green: 0.45, blue: 0.95, alpha: 1.0),
    NSColor(calibratedRed: 0.08, green: 0.18, blue: 0.38, alpha: 1.0),
])!
gradient.draw(in: bg, angle: -90)

let ringRect = rect.insetBy(dx: 148, dy: 148)
let ring = NSBezierPath(ovalIn: ringRect)
NSColor.white.withAlphaComponent(0.16).setStroke()
ring.lineWidth = 20
ring.stroke()

if let symbol = NSImage(systemSymbolName: "brain.head.profile", accessibilityDescription: nil) {
    let config = NSImage.SymbolConfiguration(pointSize: 520, weight: .regular)
    let icon = symbol.withSymbolConfiguration(config) ?? symbol
    let iconRect = NSRect(x: 252, y: 242, width: 520, height: 520)
    NSColor.white.set()
    icon.draw(in: iconRect)
}

let text = NSString(string: "am")
let paragraph = NSMutableParagraphStyle()
paragraph.alignment = .center
let attrs: [NSAttributedString.Key: Any] = [
    .font: NSFont.systemFont(ofSize: 140, weight: .black),
    .foregroundColor: NSColor.white.withAlphaComponent(0.9),
    .paragraphStyle: paragraph,
]
text.draw(in: NSRect(x: 270, y: 118, width: 484, height: 150), withAttributes: attrs)

image.unlockFocus()

guard let tiff = image.tiffRepresentation,
      let bitmap = NSBitmapImageRep(data: tiff),
      let png = bitmap.representation(using: .png, properties: [:]) else {
    fputs("failed to render icon\n", stderr)
    exit(1)
}

try png.write(to: output)
