import AppKit

let output = URL(fileURLWithPath: CommandLine.arguments[1])
try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)
for size in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let pixels = size * scale
        let image = NSImage(size: NSSize(width: 1024, height: 1024))
        image.lockFocus()
        NSColor(calibratedRed: 0.08, green: 0.18, blue: 0.29, alpha: 1).setFill()
        NSBezierPath(roundedRect: NSRect(x: 22, y: 22, width: 980, height: 980), xRadius: 210, yRadius: 210).fill()
        NSColor(calibratedRed: 0.30, green: 0.85, blue: 0.76, alpha: 1).setStroke()
        let bridge = NSBezierPath()
        bridge.lineWidth = 42
        bridge.lineCapStyle = .round
        bridge.move(to: NSPoint(x: 245, y: 315))
        bridge.line(to: NSPoint(x: 245, y: 605))
        bridge.curve(to: NSPoint(x: 780, y: 605), controlPoint1: NSPoint(x: 245, y: 925), controlPoint2: NSPoint(x: 780, y: 925))
        bridge.line(to: NSPoint(x: 780, y: 315))
        bridge.stroke()
        NSColor.white.setFill()
        NSBezierPath(roundedRect: NSRect(x: 378, y: 205, width: 268, height: 415), xRadius: 130, yRadius: 130).fill()
        NSColor(calibratedRed: 0.08, green: 0.18, blue: 0.29, alpha: 1).setFill()
        NSBezierPath(roundedRect: NSRect(x: 498, y: 466, width: 28, height: 85), xRadius: 14, yRadius: 14).fill()
        image.unlockFocus()
        let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels,
            bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
            colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
        image.draw(in: NSRect(x: 0, y: 0, width: pixels, height: pixels))
        NSGraphicsContext.restoreGraphicsState()
        let suffix = scale == 2 ? "@2x" : ""
        try bitmap.representation(using: .png, properties: [:])!.write(to: output.appendingPathComponent("icon_\(size)x\(size)\(suffix).png"))
    }
}
