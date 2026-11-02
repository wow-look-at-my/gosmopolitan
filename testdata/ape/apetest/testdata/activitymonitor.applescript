-- Opens Activity Monitor and prints each value in its window that contains
-- one of the words given as arguments, one value per line. When nothing
-- matches, it prints how many elements of each kind the window held.
on run argv
	tell application "Activity Monitor" to activate
	-- Activity Monitor samples every few seconds, so a new process shows up late.
	delay 8
	set found to {}
	set kinds to {}
	tell application "System Events"
		tell process "Activity Monitor"
			set elems to entire contents of front window
			repeat with elem in elems
				try
					set end of kinds to (class of elem) as text
				end try
				try
					set shown to (value of elem) as text
					repeat with wanted in argv
						if shown contains (wanted as text) then set end of found to shown
					end repeat
				end try
			end repeat
		end tell
	end tell
	tell application "Activity Monitor" to quit
	set AppleScript's text item delimiters to linefeed
	if found is {} then return "no match among " & (count of elems) & " elements: " & (my summary(kinds))
	return found as text
end run

-- summary counts each distinct kind in kinds.
on summary(kinds)
	set seen to {}
	set out to ""
	repeat with kind in kinds
		set kind to kind as text
		if seen does not contain kind then
			set end of seen to kind
			set total to 0
			repeat with other in kinds
				if (other as text) is kind then set total to total + 1
			end repeat
			set out to out & kind & "=" & total & " "
		end if
	end repeat
	return out
end summary
