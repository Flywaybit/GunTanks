import { readFile } from 'node:fs/promises';
for (const file of ['index.html', 'src/main.js', 'src/api.js', 'src/socket.js', 'src/renderer.js']) await readFile(file);
console.log('GunTanks client sources are ready. Static hosting requires no bundling.');
