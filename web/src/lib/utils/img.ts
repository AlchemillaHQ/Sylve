export async function canvasToPNGDownload(canvasId: string, filename?: string) {
    const canvas = document.getElementById(canvasId) as HTMLCanvasElement;
    if (!canvas) {
        console.error(`Canvas with id ${canvasId} not found`);
        return;
    }

    canvas.toBlob((blob) => {
        if (!blob) {
            console.error('Failed to convert canvas to blob');
            return;
        }

        const link = document.createElement('a');
        link.href = URL.createObjectURL(blob);
        link.download = filename || 'image.png';
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
        URL.revokeObjectURL(link.href);
    }, 'image/png');
}