# Windows Wintun.dll Setup Guide

## The Error

```
Error loading wintun.dll DLL: Unable to load library: The specified module could not be found.
```

## What is Wintun?

**Wintun** is the Windows TUN driver developed by WireGuard.  
It's required to create virtual network interfaces (TUN interfaces) on Windows.

Without it, WireGuard cannot create the tunnel interface!

## Quick Fix - Download Wintun

### Step 1: Download Wintun

**Official source:**
https://www.wintun.net/

Or direct link:
https://www.wintun.net/builds/wintun-0.14.1.zip

### Step 2: Extract the DLL

1. Download `wintun-0.14.1.zip`
2. Right-click the ZIP file and select "Extract All..."
3. Extract to a temporary location (e.g., Desktop or Downloads folder)
4. Navigate through the extracted folders:
   - **For 64-bit Windows**: `wintun\bin\amd64\wintun.dll`
   - **For 32-bit Windows**: `wintun\bin\x86\wintun.dll`
   - **For ARM64 Windows**: `wintun\bin\arm64\wintun.dll`

### Step 3: Place the DLL

**Option A: Next to the executable (Recommended)**

Navigate to your project directory:
```
E:\code\ghost-go\
```

Copy the `wintun.dll` file from the extracted folder to the `E:\code\ghost-go\` directory, placing it in the same folder as `phase1-demo.exe`.

Your directory should look like:
```
E:\code\ghost-go\
  ├── phase1-demo.exe
  └── wintun.dll          ← Place it here!
```

**Option B: System directory (Requires Administrator privileges)**

1. Right-click `wintun.dll` → Copy
2. Navigate to `C:\Windows\System32\`
3. Paste the file (you'll need Administrator permission)

### Step 4: Verify

Open File Explorer and navigate to `E:\code\ghost-go\`. Confirm that `wintun.dll` is present in the directory.

### Step 5: Run the demo

Open Command Prompt or PowerShell and run:

```powershell
.\phase1-demo.exe -role a
```

Should work now! 🎉

## Detailed Instructions

### Finding Your Windows Architecture

**Method 1: Using System Settings**

1. Press `Win + Pause/Break` or right-click "This PC" → Properties
2. Look at "System type"
   - "64-bit operating system" → Use `amd64\wintun.dll`
   - "32-bit operating system" → Use `x86\wintun.dll`

**Method 2: Using Settings**

1. Open Settings (Win + I)
2. Go to System → About
3. Look under "Device specifications"
   - "64-bit" or "x64" → Use amd64
   - "32-bit" or "x86" → Use x86

**Method 3: Using Command Prompt**

```cmd
wmic os get osarchitecture
```

This will display either:
- `64-bit` → Use `amd64\wintun.dll`
- `32-bit` → Use `x86\wintun.dll`

### Download and Extract - Manual Steps

**Step 1: Download**

1. Go to https://www.wintun.net/
2. Click the "Download" button
3. Save `wintun-0.14.1.zip` to your Downloads folder (or any location)

**Step 2: Extract**

1. Navigate to where you saved the ZIP file
2. Right-click `wintun-0.14.1.zip`
3. Select "Extract All..."
4. Choose a destination (e.g., Desktop)
5. Click "Extract"

**Step 3: Find the Correct DLL**

After extraction, you'll have a folder structure like:
```
wintun/
  └── wintun/
      └── bin/
          ├── amd64/
          │   └── wintun.dll    ← For 64-bit Windows
          ├── x86/
          │   └── wintun.dll    ← For 32-bit Windows
          └── arm64/
              └── wintun.dll    ← For ARM64 Windows
```

Navigate to the folder matching your architecture and locate `wintun.dll`.

**Step 4: Copy to Project Directory**

1. Right-click the correct `wintun.dll` file
2. Select "Copy"
3. Navigate to your project directory (`E:\code\ghost-go\`)
4. Right-click in the folder
5. Select "Paste"

**Step 5: Verify Installation**

In File Explorer, confirm you see:
```
E:\code\ghost-go\
  ├── phase1-demo.exe
  ├── wintun.dll
  └── (other project files)
```

## Troubleshooting

### Error: "DLL is not a valid Win32 application"

**Problem:** Wrong architecture (e.g., using 32-bit DLL on 64-bit Windows)

**Solution:**
1. Check your Windows architecture (see "Finding Your Windows Architecture" above)
2. Download and extract the correct DLL:
   - 64-bit → `amd64\wintun.dll`
   - 32-bit → `x86\wintun.dll`
   - ARM64 → `arm64\wintun.dll`

### Error: "Access denied"

**Problem:** Insufficient permissions to load driver

**Solution:**
- Run the application as Administrator:
  1. Right-click `phase1-demo.exe`
  2. Select "Run as administrator"
  3. Or open Command Prompt as Administrator:
     - Search for "Command Prompt"
     - Right-click → "Run as administrator"
     - Navigate to `E:\code\ghost-go\`
     - Run `.\phase1-demo.exe -role a`

### Error: Still can't find DLL

**Check DLL location:**

1. Open File Explorer
2. Navigate to `E:\code\ghost-go\`
3. Look for `wintun.dll` in the file list
4. If not present, repeat the extraction and copy steps

**Verify file properties:**

1. Right-click `wintun.dll`
2. Select "Properties"
3. Check:
   - **File size**: Should be approximately 170 KB (for amd64 version)
   - **Type**: "Application extension" or "DLL file"
   - **Location**: Should be in your project directory

### Verify DLL is correct version

**Check file properties:**

1. Right-click `wintun.dll` → Properties
2. Go to the "Details" tab
3. Look for:
   - **File version**: Should be `0.14.1` or similar
   - **Product name**: Wintun
   - **Size**: ~170 KB (amd64), ~140 KB (x86)

### Windows Defender blocking?

Sometimes antivirus blocks driver DLLs. If you suspect this:

**Method 1: Temporarily disable real-time protection**

1. Open Windows Security
2. Go to "Virus & threat protection"
3. Click "Manage settings"
4. Turn off "Real-time protection" (temporary)
5. Try running the demo again
6. Re-enable protection after testing

**Method 2: Add folder to exclusions**

1. Open Windows Security
2. Go to "Virus & threat protection"
3. Click "Manage settings"
4. Scroll to "Exclusions"
5. Click "Add or remove exclusions"
6. Click "Add an exclusion" → "Folder"
7. Select `E:\code\ghost-go\`

### Multiple wintun.dll files?

If you have multiple copies of wintun.dll in different locations, Windows might load the wrong one.

**Solution:**

1. Remove any wintun.dll files from system directories (if you placed them there)
2. Keep only the one in `E:\code\ghost-go\` (next to your executable)
3. This ensures the correct DLL is loaded

## Alternative: Install WireGuard for Windows

If you have **WireGuard for Windows** installed, wintun.dll should already be on your system.

**Steps:**

1. Go to https://www.wireguard.com/install/
2. Download WireGuard for Windows
3. Run the installer (requires Administrator)
4. Follow the installation wizard
5. After installation, wintun.dll is installed system-wide

**After installing WireGuard:**

The demo should work without manually placing the DLL, as WireGuard installs it to a system directory that's in the PATH.

## Security Note

**Always download wintun.dll from official sources:**
- ✅ https://www.wintun.net/
- ✅ https://git.zx2c4.com/wintun/
- ❌ Random third-party sites (may contain malware!)

The official WireGuard project maintains wintun.net.

## Verification After Install

**Step 1: Check DLL is in place**

In File Explorer:
1. Navigate to `E:\code\ghost-go\`
2. Confirm `wintun.dll` is visible in the folder

**Step 2: Try running the demo**

Open Command Prompt or PowerShell:

```powershell
cd E:\code\ghost-go
.\phase1-demo.exe -role a
```

You should see progress beyond the wintun.dll error. If you encounter other errors, those are unrelated to wintun.dll installation.

**Step 3: Check application logs**

If the demo still fails, look for error messages in the console output to diagnose other issues.

## For Linux/macOS Users

**You don't need wintun.dll!**

This is Windows-only. Linux and macOS have native TUN/TAP support:
- **Linux**: Uses kernel TUN driver
- **macOS**: Uses built-in utun interfaces

Just run the demo directly (with sudo for Linux/macOS):

**Linux:**
```bash
sudo ./demo -role a
```

**macOS:**
```bash
sudo ./demo -role a
```

## Summary Checklist

- [ ] Downloaded wintun-0.14.1.zip from official source
- [ ] Determined correct Windows architecture (amd64/x86/arm64)
- [ ] Extracted the ZIP file
- [ ] Copied correct wintun.dll to project directory
- [ ] Verified wintun.dll is present in E:\code\ghost-go\
- [ ] Ran phase1-demo.exe and confirmed wintun.dll error is resolved

---

**After installing wintun.dll, the demo should get past TUN creation! 🚀**
