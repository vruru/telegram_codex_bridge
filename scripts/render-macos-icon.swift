import AppKit
import Foundation

let outputDirectory = CommandLine.arguments.dropFirst().first ?? ""
guard !outputDirectory.isEmpty else {
    fputs("usage: swift render-macos-icon.swift <iconset-dir>\n", stderr)
    exit(1)
}

let fileManager = FileManager.default
try fileManager.createDirectory(atPath: outputDirectory, withIntermediateDirectories: true)

let entries: [(String, CGFloat)] = [
    ("icon_16x16.png", 16),
    ("icon_16x16@2x.png", 32),
    ("icon_32x32.png", 32),
    ("icon_32x32@2x.png", 64),
    ("icon_128x128.png", 128),
    ("icon_128x128@2x.png", 256),
    ("icon_256x256.png", 256),
    ("icon_256x256@2x.png", 512),
    ("icon_512x512.png", 512),
    ("icon_512x512@2x.png", 1024),
]

for (name, size) in entries {
    let image = renderIcon(size: size)
    let path = (outputDirectory as NSString).appendingPathComponent(name)
    try writePNG(image: image, to: path)
}

func renderIcon(size: CGFloat) -> NSImage {
    let image = NSImage(size: NSSize(width: size, height: size))
    image.lockFocus()

    let rect = NSRect(x: 0, y: 0, width: size, height: size)
    NSColor.clear.setFill()
    rect.fill()

    let circleRect = rect.insetBy(dx: size * 0.08, dy: size * 0.08)
    let blue = NSColor(calibratedRed: 0.16, green: 0.62, blue: 0.97, alpha: 1.0)
    let blueDark = NSColor(calibratedRed: 0.11, green: 0.49, blue: 0.90, alpha: 1.0)

    let gradient = NSGradient(starting: blue, ending: blueDark)
    let circle = NSBezierPath(ovalIn: circleRect)
    gradient?.draw(in: circle, angle: -90)

    NSColor.white.withAlphaComponent(0.18).setStroke()
    circle.lineWidth = max(2, size * 0.018)
    circle.stroke()

    let plane = NSBezierPath()
    plane.move(to: CGPoint(x: size * 0.26, y: size * 0.49))
    plane.line(to: CGPoint(x: size * 0.78, y: size * 0.72))
    plane.line(to: CGPoint(x: size * 0.50, y: size * 0.27))
    plane.line(to: CGPoint(x: size * 0.46, y: size * 0.46))
    plane.line(to: CGPoint(x: size * 0.31, y: size * 0.50))
    plane.close()
    NSColor.white.setFill()
    plane.fill()

    let fold = NSBezierPath()
    fold.move(to: CGPoint(x: size * 0.46, y: size * 0.46))
    fold.line(to: CGPoint(x: size * 0.58, y: size * 0.37))
    fold.lineWidth = max(2, size * 0.04)
    fold.lineCapStyle = .round
    fold.lineJoinStyle = .round
    NSColor(calibratedWhite: 1.0, alpha: 0.75).setStroke()
    fold.stroke()

    image.unlockFocus()
    return image
}

func writePNG(image: NSImage, to path: String) throws {
    guard
        let tiffData = image.tiffRepresentation,
        let bitmap = NSBitmapImageRep(data: tiffData),
        let png = bitmap.representation(using: .png, properties: [:])
    else {
        throw NSError(domain: "render-macos-icon", code: 1, userInfo: [NSLocalizedDescriptionKey: "failed to encode png"])
    }

    try png.write(to: URL(fileURLWithPath: path))
}
