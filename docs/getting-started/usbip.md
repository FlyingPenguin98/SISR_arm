
# 🔌 USBIP

=== "🪟 Windows"

    [usbip-win2](https://github.com/vadimgrn/usbip-win2) is by far the most complete implementation of USBIP for Windows (comes with a **SIGNED** kernel mode driver).

    **Install and done 😉**  

    Be sure to grab the **latest** version!

    !!! info "Windows on ARM"
        Use the **ARM64** installer (`USBip-<version>-ARM64.exe`, v0.9.7.8 or newer).  
        Kernel mode drivers can not run under emulation, so the x64 installer will not work on ARM devices.

=== "🐧 Linux"

    ### 🏹 Arch Linux

    ```bash
    sudo pacman -S usbip
    ```

    [Arch Wiki: USBIP](https://wiki.archlinux.org/title/USB/IP)

    ??? tip "Steam OS users"
        If you are installing SISR on Steam OS, you have to switch to the desktop mode and enable write access to the root filesystem first:

        ```bash
        sudo steamos-readonly disable
        ```

    ### 🟠 Ubuntu/Debian

    ```bash
    sudo apt install linux-tools-generic
    ```

    [Ubuntu USBIP Manual](https://manpages.ubuntu.com/manpages/noble/man8/usbip.8.html)

    ### 🧩 Linux Kernel Module Setup

    !!! info "USBIP Client Requirement"
        USBIP requires the `vhci-hcd` (Virtual Host Controller Interface) kernel module on Linux.  
        Most Linux distributions include this module but don't load it automatically.

    #### 🧷 One-Time Setup

    To load the module automatically on boot:

    ```bash
    echo "vhci-hcd" | sudo tee /etc/modules-load.d/vhci-hcd.conf
    sudo modprobe vhci-hcd
    ```

    #### 🔄 Manual Loading

    To load the module for the current session only:

    ```bash
    sudo modprobe vhci-hcd
    ```

    #### 🔎 Verification

    Check if the module is loaded:

    ```bash
    lsmod | grep vhci_hcd
    ```
