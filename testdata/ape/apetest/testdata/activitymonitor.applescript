-- Opens Activity Monitor and prints each process name in its table that
-- contains one of the words given as arguments, one name per line.
on run argv
	tell application "Activity Monitor" to activate
	-- Activity Monitor samples every few seconds, so a new process shows up late.
	delay 8
	set found to {}
	tell application "System Events"
		tell process "Activity Monitor"
			repeat with elem in (entire contents of window 1)
				try
					if class of elem is static text then
						set shown to value of elem
						repeat with wanted in argv
							if shown contains (wanted as text) then set end of found to shown
						end repeat
					end if
				end try
			end repeat
		end tell
	end tell
	tell application "Activity Monitor" to quit
	set AppleScript's text item delimiters to linefeed
	return found as text
end run
