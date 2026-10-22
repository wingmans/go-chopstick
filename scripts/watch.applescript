on surfaceConfig(projectDirectory, makeTarget)
  tell application "Ghostty"
    set paneConfig to new surface configuration
    set initial working directory of paneConfig to projectDirectory
    set initial input of paneConfig to "make " & makeTarget & linefeed

    return paneConfig
  end tell
end surfaceConfig

on run arguments
  if (count of arguments) is not 1 then
    error "usage: watch.applescript <project-directory>"
  end if

  set projectDirectory to item 1 of arguments

  tell application "Ghostty"
    set consoleWindow to new window with configuration my surfaceConfig(projectDirectory, "dev")
    set consoleTab to selected tab of consoleWindow
    set devTerminal to focused terminal of consoleTab

    -- Split the full window first to reserve the bottom overview row.
    set overviewTerminal to split devTerminal direction down with configuration my surfaceConfig(projectDirectory, "overview")
    set errorsTerminal to split overviewTerminal direction right with configuration my surfaceConfig(projectDirectory, "logs-errors")

    -- Divide the upper area into service, discovery, download, and process.
    set discoveryTerminal to split devTerminal direction right with configuration my surfaceConfig(projectDirectory, "logs-discovery")
    set downloadTerminal to split devTerminal direction down with configuration my surfaceConfig(projectDirectory, "logs-download")
    set processTerminal to split discoveryTerminal direction down with configuration my surfaceConfig(projectDirectory, "logs-process")

    focus devTerminal
    activate window consoleWindow
  end tell
end run
