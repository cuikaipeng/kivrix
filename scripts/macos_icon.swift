// Prepare the legacy ICNS artwork used by both the app and the DMG volume.
// Keep the original logo intact; render only into the temporary build snapshot.
import AppKit

guard CommandLine.arguments.count == 3,
      let image = NSImage(contentsOfFile: CommandLine.arguments[1]),
      let bitmap = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: 1024, pixelsHigh: 1024,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0),
      let context = NSGraphicsContext(bitmapImageRep: bitmap) else {
    fatalError("Usage: swift macos_icon.swift <source.png> <output.png>")
}

// A transparent margin keeps Finder's unmasked volume icon from looking oversized.
let bounds = NSRect(x: 0, y: 0, width: 1024, height: 1024)
let artwork = bounds.insetBy(dx: 100, dy: 100)
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = context
context.cgContext.clear(bounds)
context.imageInterpolation = .high
NSBezierPath(roundedRect: artwork, xRadius: 185, yRadius: 185).addClip()
image.draw(in: artwork, from: .zero, operation: .copy, fraction: 1)
NSGraphicsContext.restoreGraphicsState()

guard let png = bitmap.representation(using: .png, properties: [:]) else {
    fatalError("Could not encode the macOS icon")
}
try png.write(to: URL(fileURLWithPath: CommandLine.arguments[2]))
