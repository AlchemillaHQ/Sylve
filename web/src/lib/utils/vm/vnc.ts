/*
From the man page:
    w=width and h=height
        A display resolution, width and height,  respectively.   If
        not specified, a default resolution of 1024x768 pixels will
        be  used.   Minimal supported resolution is 640x480 pixels,
        and maximum is 3840x2160 pixels.
*/
export const resolutions = [
    // 4:3
    { label: '640x480', value: '640x480' },
    { label: '800x600', value: '800x600' },
    { label: '1024x768', value: '1024x768' },
    { label: '1152x864', value: '1152x864' },
    { label: '1280x960', value: '1280x960' },
    { label: '1400x1050', value: '1400x1050' },
    { label: '1600x1200', value: '1600x1200' },

    // 16:9
    { label: '1280x720', value: '1280x720' },
    { label: '1366x768', value: '1366x768' },
    { label: '1600x900', value: '1600x900' },
    { label: '1920x1080', value: '1920x1080' },
    { label: '2560x1440', value: '2560x1440' },
    { label: '3840x2160', value: '3840x2160' },

    // 16:10
    { label: '1280x800', value: '1280x800' },
    { label: '1440x900', value: '1440x900' },
    { label: '1680x1050', value: '1680x1050' },
    { label: '1920x1200', value: '1920x1200' },
    { label: '2560x1600', value: '2560x1600' },

    // Ultrawide (optional but valid within limits)
    { label: '2560x1080', value: '2560x1080' },
    { label: '3440x1440', value: '3440x1440' }
];
