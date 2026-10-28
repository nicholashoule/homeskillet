# ~/.bashrc: bash reads it in every interactive shell.

# Stop here in a shell that is not interactive, such as one scp starts.
case $- in
*i*) ;;
*) return ;;
esac

HISTSIZE=10000
HISTCONTROL=ignoreboth
shopt -s histappend checkwinsize

# Programs installed in ~/.local/bin, homeskillet among them, appended so
# nothing there shadows a system command.
case ":$PATH:" in
*":$HOME/.local/bin:"*) ;;
*) PATH="$PATH:$HOME/.local/bin" ;;
esac

alias ll='ls -alF'

command -v homeskillet >/dev/null && eval "$(homeskillet completion bash)"
