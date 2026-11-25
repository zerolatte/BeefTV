#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const args = process.argv.slice(2);
let project = 'beeftv-platform';
const services = [];
for (let i = 0; i < args.length; i++) {
  if (args[i] === '--project-name') {
    project = args[++i];
  } else if (args[i] === 'backend' || args[i] === 'web') {
    services.push(args[i]);
  } else {
    console.error('用法：node scripts/deploy-local.mjs [backend|web] [--project-name 项目名]');
    process.exit(1);
  }
}
if (typeof project !== 'string' || !/^[a-z0-9][a-z0-9_-]*$/.test(project)) {
  console.error('Compose 项目名无效。');
  process.exit(1);
}
if (!services.length) services.push('backend', 'web');
const compose = ['compose', '-p', project, '-f', 'docker-compose.local.yml'];
function docker(command, failure) {
  const result = spawnSync('docker', command, { cwd: root, stdio: 'inherit' });
  if (result.error || result.status !== 0) {
    console.error(failure);
    if (result.error) console.error(result.error.message);
    process.exit(result.status || 1);
  }
}

docker([...compose, 'build', ...services], '镜像构建失败，保留现有部署与镜像。');
// 后端容器可能换 IP，同时重建前端使 Nginx 重新解析服务地址。
const deployServices = services.includes('backend') ? ['backend', 'web'] : ['web'];
docker([...compose, 'up', '-d', '--no-build', '--no-deps', '--force-recreate', '--wait', '--wait-timeout', '120', ...deployServices],
  '服务未通过健康检查，保留旧镜像以便排查。');
// 只清理本项目无标签且未被任何容器使用的旧镜像，不清理数据卷或构建缓存。
docker(['image', 'prune', '--force', '--filter', `label=com.docker.compose.project=${project}`],
  '服务已更新，但旧镜像清理失败。');
docker([...compose, 'ps'], '读取服务状态失败。');
console.log('本地部署完成，旧镜像已清理。');
