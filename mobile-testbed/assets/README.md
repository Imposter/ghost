# Ghost Testbed Assets

This directory should contain the following image assets:

- `icon.png` - App icon (1024x1024)
- `splash.png` - Splash screen image (1284x2778 recommended)
- `adaptive-icon.png` - Android adaptive icon foreground (1024x1024)
- `favicon.png` - Web favicon (48x48)

For development, you can use placeholder images or generate them using:
```bash
# Using ImageMagick to create placeholder icons
convert -size 1024x1024 xc:#1a1a1a -fill '#4CAF50' -draw "circle 512,512 512,100" icon.png
convert -size 1284x2778 xc:#1a1a1a splash.png
convert -size 1024x1024 xc:transparent -fill '#4CAF50' -draw "circle 512,512 512,100" adaptive-icon.png
convert -size 48x48 xc:#1a1a1a -fill '#4CAF50' -draw "circle 24,24 24,4" favicon.png
```
