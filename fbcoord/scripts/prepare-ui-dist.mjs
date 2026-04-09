import { cp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const rootDir = path.dirname(fileURLToPath(import.meta.url));
const projectDir = path.resolve(rootDir, '..');
const sourceDir = path.join(projectDir, 'ui');
const distDir = path.join(sourceDir, 'dist');

await rm(distDir, { recursive: true, force: true });
await mkdir(distDir, { recursive: true });

for (const file of ['index.html', 'styles.css']) {
  const content = await readFile(path.join(sourceDir, file), 'utf8');
  await writeFile(path.join(distDir, file), content, 'utf8');
}

await cp(path.join(sourceDir, 'icons'), path.join(distDir, 'icons'), { recursive: true });
