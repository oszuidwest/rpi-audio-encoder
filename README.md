# rpi-encoder
Can we replace our bulky rackmount audio encoder with a Raspberry Pi? This project tries to explore that.

⚠️ Highly experimental. Don't prod this now. Maybe ever. ⚠️

# How to prepare the Rapsberry Pi
- Install Raspberry Pi OS Lite 11 (bullseye) 32-bit
- Run 'sudo raspi-config' to set timezone, Wi-Fi country and expand the filesystem
- Follow the guide on https://www.hifiberry.com/docs/software/configuring-linux-3-18-x/ to set-up the HiFiBerry

## Clean-up image
`sudo apt remove bluez* build-essential bzip2 cifs-utils cpp dbus dmidecode dosfstools eject gcc gcc-7-base gcc-8-base gcc-9-base gdb gdisk iw libcamera-apps-lite manpages manpages-dev mksh ntfs-3g p7zip* pi-bluetooth vim-common vim-tiny wireless-regdb wireless-tools wpasupplicant xauth -y`

## Update everything that's left
`sudo apt autoremove -y; sudo apt update -y; sudo apt upgrade -y; sudo apt dist-upgrade -y;`

## Install tools 
`sudo apt install ffmpeg supervisor -y`

## Set-up ffmpeg
`wget https://raw.githubusercontent.com/oszuidwest/rpi-encoder/main/stream.conf -O /etc/supervisor/conf.d/stream.conf`    
