# Playing the game through MapleGate (macOS)

This sends your game's traffic through a server in the US west. It only runs while you ask it to, and it needs one approval each time.

## Install (once)

1. Download `MapleGate.pkg` from the link you were given.
2. The package is not signed by Apple, so macOS refuses a double click. **Right click the file, choose Open, then Open again.** If that button is missing: System Settings, Privacy & Security, scroll down, "Open Anyway".
3. Enter your Mac password when asked. A grey dot appears in the menu bar. (MapleGate does not appear under System Settings, VPN; the dot is where it lives.)
4. You will get an e-mail from AWS inviting you to set a password. Do that first.

## Play

1. Click the dot, then **Connect**. A browser opens the first time: sign in with the account from the invitation. Later it does not open again.
2. Wait. The person who runs the server gets a message and presses Approve. The menu says what it is waiting for.
3. When the dot is green and a notice says Connected, start the game.
4. When you are done, click the dot, then **Disconnect**. If you forget, the server notices after about 10 minutes of no use and stops by itself.

Colours: grey, not connected; amber, signing in or waiting for approval; green, connected; red, something went wrong (the menu says what).

## If something is wrong

- Choose **Copy diagnostics** in the menu, then paste into your message to the person who runs the server. It holds the menu text, the tunnel's state, your routes and the last lines of the log, and no password or key.
- Red with "denied": the approval was refused. Ask.
- Red with "expired": nobody approved in 15 minutes. Connect again and tell them.
- Nothing happens on Connect: choose Quit in the menu (this also disconnects) and open **MapleGate** again from Applications or Spotlight. If the menu says the VPN service is not running, restart the Mac once.
- The game works but is slow: tell the person who runs the server which country you are in.

## Remove

Open `MapleGate-uninstall.pkg` the same way you opened the installer. Or, in Terminal, run `sudo /usr/local/share/maplegate/uninstall.sh`.
