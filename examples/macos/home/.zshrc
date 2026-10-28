# ~/.zshrc: zsh, the macOS default shell, reads it in every interactive shell.

HISTFILE=~/.zsh_history
HISTSIZE=10000
SAVEHIST=10000
setopt share_history hist_ignore_dups

autoload -Uz compinit && compinit

alias ll='ls -alF'

command -v homeskillet >/dev/null && eval "$(homeskillet completion zsh)"
