-- Opens Activity Monitor, finds its process table by its accessibility role,
-- and prints the text of each row that contains one of the words given as
-- arguments, one row per line. It reads only the table's rows: a walk of
-- every element in the window takes minutes.
on run argv
	tell application "Activity Monitor" to activate
	-- Activity Monitor samples every few seconds, so a new process shows up late.
	delay 8
	set found to {}
	tell application "System Events"
		tell process "Activity Monitor"
			set frontmost to true
			-- The search field keeps only the processes whose name holds the first word.
			keystroke "f" using command down
			keystroke (item 1 of argv)
			delay 4
			-- table, cell, kind and text name System Events classes and properties, so no variable takes them.
			set procTable to my findTable(front window, 0)
			if procTable is missing value then
				set childRoles to {}
				repeat with child in (UI elements of front window)
					try
						set end of childRoles to (role of child) as text
					end try
				end repeat
				set AppleScript's text item delimiters to " "
				return "no table in the window; its children: " & (childRoles as text)
			end if
			set total to count of rows of procTable
			repeat with tableRow in rows of procTable
				repeat with shown in my rowTexts(tableRow)
					repeat with wanted in argv
						if (shown as text) contains (wanted as text) then set end of found to (shown as text)
					end repeat
				end repeat
			end repeat
		end tell
	end tell
	tell application "Activity Monitor" to quit
	set AppleScript's text item delimiters to linefeed
	if found is {} then return "no match among " & total & " rows"
	return found as text
end run

-- findTable returns the first outline or table under elem, or missing value.
on findTable(elem, depth)
	if depth > 8 then return missing value
	tell application "System Events"
		try
			set elemRole to role of elem
			if elemRole is "AXOutline" or elemRole is "AXTable" then return elem
		end try
		try
			repeat with child in (UI elements of elem)
				set hit to my findTable(child, depth + 1)
				if hit is not missing value then return hit
			end repeat
		end try
	end tell
	return missing value
end findTable

-- rowTexts returns the value, name and description of each cell in tableRow,
-- and of each element inside a cell.
on rowTexts(tableRow)
	set collected to {}
	tell application "System Events"
		repeat with rowCell in (UI elements of tableRow)
			set collected to collected & my elemTexts(rowCell)
			try
				repeat with inner in (UI elements of rowCell)
					set collected to collected & my elemTexts(inner)
				end repeat
			end try
		end repeat
	end tell
	return collected
end rowTexts

-- elemTexts returns whichever of value, name and description elem has.
on elemTexts(elem)
	set collected to {}
	tell application "System Events"
		try
			set end of collected to (value of elem) as text
		end try
		try
			set end of collected to (name of elem) as text
		end try
		try
			set end of collected to (description of elem) as text
		end try
	end tell
	return collected
end elemTexts
