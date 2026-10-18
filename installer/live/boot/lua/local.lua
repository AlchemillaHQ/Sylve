local core = require("core")
local drawer = require("drawer")
local menu = require("menu")
local screen = require("screen")

if not core.isFramebufferConsole() or core.isSerialConsole() or
    not gfx.fb_putimage or core.loaderTooOld() or
    (loader.getenv("loader_gfx") or "yes"):lower() == "no" then
	return
end

local stock = menu.welcome.all_entries
local function back(name)
	return {entry_type = core.MENU_RETURN, name = name .. " [Backspace]"}
end

local advanced = {
	entries = {
		back("Back to boot options"),
		stock.console,
		{entry_type = core.MENU_SEPARATOR, name = "Kernel:"},
		stock.kernel_options,
		stock.zpool_checkpoints,
		stock.boot_envs,
		stock.chainload,
		stock.loader_needs_upgrade,
	},
}

local options = {entries = {}}
for index, entry in ipairs(menu.boot_options.entries) do
	options.entries[index] = entry
end
options.entries[1] = back("Back to installer menu")
options.entries[#options.entries + 1] = {entry_type = core.MENU_SEPARATOR}
options.entries[#options.entries + 1] = {
	entry_type = core.MENU_SUBMENU,
	name = "Console, kernel and boot environments",
	submenu = advanced,
	alias = {"c", "C"},
}

local boot_options = {
	entry_type = core.MENU_SUBMENU,
	name = "Boot options [O]",
	submenu = options,
	alias = {"o", "O"},
}

local main = {
	entries = function()
		local boot_entries = menu.welcome.entries()
		return {
			boot_entries[1],
			boot_entries[2],
			boot_options,
			stock.reboot,
			stock.prompt,
		}
	end,
}
menu.default = main

local images = "/boot/images/sylve-menu/"
local frame
local selections = {[main] = 1}
local row_y = {248, 322, 392, 462, 532}
local rendered_image
local drawscreen = drawer.drawscreen
local autoboot = menu.autoboot
local run = menu.run

local function render_without_spinner(render, ...)
	local verbose = loader.getenv("module_verbose") or "2"
	local divisor = loader.getenv("twiddle_divisor") or "16"
	loader.setenv("module_verbose", "1")
	local ok, result = pcall(render, ...)
	loader.setenv("twiddle_divisor", divisor)
	loader.setenv("module_verbose", verbose)
	if not ok then
		error(result, 0)
	end
	return result
end

local function putimage(name, x, y, width, height)
	return render_without_spinner(gfx.fb_putimage, images .. name .. ".png",
	    frame.x + x * frame.width // 1024,
	    frame.y + y * frame.height // 768,
	    frame.x + (x + width) * frame.width // 1024,
	    frame.y + (y + height) * frame.height // 768, 0)
end

local function footer(name)
	putimage(name, 264, 624, 496, 32)
end

local function visible_entries(menudef)
	local source = menudef.entries
	if type(source) == "function" then
		source = source()
	end
	local entries = {}
	local line = 0
	for _, entry in ipairs(source) do
		if entry.visible == nil or entry.visible() then
			line = line + 1
			if entry.entry_type ~= core.MENU_SEPARATOR then
				entries[#entries + 1] = {entry = entry, line = line}
			end
		end
	end
	return entries
end

local function draw_selection(menudef, entries, previous)
	local index = selections[menudef] or 1
	if frame ~= nil and menudef == main then
		if previous ~= nil and previous ~= index then
			putimage(rendered_image .. "-row-" .. previous,
			    260, row_y[previous], 504, 68)
		end
		if previous ~= index then
			putimage(rendered_image .. "-row-" .. index .. "-selected",
			    260, row_y[index], 504, 68)
		end
	else
		if previous ~= nil and entries[previous] ~= nil then
			screen.setcursor(20, 10 + entries[previous].line)
			printc(" ")
		end
		if entries[index] ~= nil then
			screen.setcursor(20, 10 + entries[index].line)
			printc(">")
		end
	end
end

local function draw_native(menudef)
	local aliases = drawscreen(menudef)
	printc(core.KEYSTR_CSI .. "?25l")
	draw_selection(menudef, visible_entries(menudef))
	return aliases
end

local function render_menu(menudef)
	frame = nil
	if menudef ~= main or not core.isFramebufferConsole() or
	    core.isSerialConsole() then
		return draw_native(menudef)
	end

	local width = tonumber(loader.getenv("screen.width")) or 0
	local height = tonumber(loader.getenv("screen.height")) or 0
	if width < 640 or height < 480 then
		return draw_native(menudef)
	end
	local image_width, image_height = 1024, 768
	if width - 2 < image_width then
		image_width = width - 2
		image_height = image_width * 768 // 1024
	end
	if height - 2 < image_height then
		image_height = height - 2
		image_width = image_height * 1024 // 768
	end
	frame = {
		x = (width - image_width) // 2,
		y = (height - image_height) // 2,
		width = image_width,
		height = image_height,
	}
	local image = core.isSingleUserBoot() and "recovery" or "main"
	if not putimage(image, 0, 0, 1024, 768) then
		frame = nil
		return draw_native(menudef)
	end
	rendered_image = image
	printc(core.KEYSTR_CSI .. "?25l")
	draw_selection(main, visible_entries(main), 1)

	local aliases = {}
	for index, entry in ipairs(main.entries()) do
		aliases[tostring(index)] = entry
		for _, alias in ipairs(entry.alias or {}) do
			aliases[alias] = entry
		end
	end
	return aliases
end

function drawer.drawscreen(menudef)
	return render_without_spinner(render_menu, menudef)
end

local function read_sequence_byte()
	for _ = 1, 100 do
		if io.ischar() then
			return io.getchar()
		end
		loader.delay(1000)
	end
	return nil
end

local function read_key(initial)
	local key = initial
	if key == nil then
		key = io.getchar()
	end
	if key ~= 27 then
		return key
	end
	local prefix = read_sequence_byte()
	if prefix == nil or prefix == 27 then
		return 27
	end
	if prefix ~= 91 and prefix ~= 79 then
		return "ignored"
	end
	local parameters = ""
	for _ = 1, 16 do
		local byte = read_sequence_byte()
		if byte == nil then
			return "ignored"
		end
		if byte >= 64 and byte <= 126 then
			if byte == 65 or byte == 90 then
				return "up"
			elseif byte == 66 then
				return "down"
			elseif byte == 72 then
				return "home"
			elseif byte == 70 then
				return "end"
			elseif byte == 126 then
				if parameters == "1" or parameters == "7" then
					return "home"
				elseif parameters == "4" or parameters == "8" then
					return "end"
				elseif parameters == "3" then
					return core.KEY_DELETE
				end
			end
			return "ignored"
		end
		parameters = parameters .. string.char(byte)
	end
	return "ignored"
end

function menu.process(menudef, keypress)
	local entries = visible_entries(menudef)
	if #entries == 0 then
		return
	end
	local index = selections[menudef] or 1
	if index > #entries then
		index = 1
	end
	selections[menudef] = index
	menu.draw(menudef)
	while true do
		local key = read_key(keypress)
		keypress = nil
		local next_index
		if key == "up" then
			next_index = (index + #entries - 2) % #entries + 1
		elseif key == "down" or key == 9 then
			next_index = index % #entries + 1
		elseif key == "home" then
			next_index = 1
		elseif key == "end" then
			next_index = #entries
		end
		if next_index ~= nil then
			local previous = index
			index = next_index
			selections[menudef] = index
			draw_selection(menudef, entries, previous)
		elseif menudef ~= main and
		    (key == 27 or key == core.KEY_BACKSPACE or key == core.KEY_DELETE) then
			return
		else
			local entry
			if key == core.KEY_ENTER then
				entry = entries[index].entry
			elseif type(key) == "number" and key >= 0 and key <= 255 then
				entry = menu.current_alias_table[string.char(key)]
			end
			if entry ~= nil then
				for candidate, item in ipairs(entries) do
					if item.entry == entry then
						index = candidate
						selections[menudef] = index
						break
					end
				end
				if menu.handlers[entry.entry_type](menudef, entry) == false then
					return
				end
				entries = visible_entries(menudef)
				if index > #entries then
					index = 1
				end
				selections[menudef] = index
				menu.draw(menudef)
			end
		end
	end
end

function menu.autoboot(delay)
	if frame == nil then
		return autoboot(delay)
	end
	local deadline = loader.time() + delay
	local last
	while true do
		local remaining = deadline - loader.time()
		if remaining < 0 then
			remaining = 0
		end
		if remaining ~= last then
			footer(remaining <= 15 and "countdown-" .. remaining or "autoboot")
			last = remaining
		end
		if io.ischar() then
			local key = io.getchar()
			if key ~= core.KEY_ENTER then
				footer("keyboard")
				return key
			end
			break
		end
		if remaining == 0 then
			break
		end
		loader.delay(50000)
	end
	cli_execute_unparsed(loader.getenv("menu_timeout_command") or "boot")
	return nil
end

function menu.run()
	selections[main] = 1
	local result = run()
	screen.clear()
	screen.setcursor(1, 1)
	printc(core.KEYSTR_CSI .. "?25h")
	return result
end
