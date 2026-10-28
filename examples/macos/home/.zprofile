# ~/.zprofile: zsh reads it in login shells, and every Terminal window is one.

# Homebrew. On Apple silicon it lives in /opt/homebrew, which is not on PATH
# until brew shellenv adds it; on Intel Macs, /usr/local/bin already is.
if [[ -x /opt/homebrew/bin/brew ]]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# Programs installed in ~/.local/bin, homeskillet among them, appended so
# nothing there shadows a system command.
typeset -U path
path+=("$HOME/.local/bin")
