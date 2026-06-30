#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import re
import sys
import json
import datetime
from pathlib import Path
import html as html_module


FILE_ICON_EMOJIS = {
    'folder': '📁',
    'parent': '⬆️',
    'disc': '💿',
    'archive': '📦',
    'pdf': '📕',
    'doc': '📘',
    'xls': '📗',
    'ppt': '📙',
    'code': '💻',
    'binary': '⚙️',
    'package': '📦',
    'executable': '⚡',
    'image': '🖼️',
    'text': '📝',
    'data': '📊',
    'model': '🧠',
    'video': '🎬',
    'build': '🔧',
    '3d': '🧊',
    'file': '📄',
}

FILE_EXT_BADGE_COLORS = {
    'iso': '#dc2626',
    'img': '#dc2626',
    '7z': '#7c3aed',
    'zip': '#7c3aed',
    'gz': '#7c3aed',
    'xz': '#7c3aed',
    'tar': '#7c3aed',
    'pdf': '#dc2626',
    'c': '#16a34a',
    'h': '#16a34a',
    'cpp': '#16a34a',
    'py': '#2563eb',
    'sh': '#16a34a',
    'bin': '#6b7280',
    'deb': '#d97706',
    'exe': '#dc2626',
    'png': '#0d9488',
    'jpg': '#0d9488',
    'svg': '#0d9488',
    'txt': '#6b7280',
    'md': '#6b7280',
    'csv': '#16a34a',
    'json': '#16a34a',
    'onnx': '#7c3aed',
    'rknn': '#7c3aed',
    'pth': '#7c3aed',
    'h264': '#dc2626',
    'mp4': '#dc2626',
    'stl': '#d97706',
    '3mf': '#d97706',
}


class DirectoryGenerator:
    def __init__(self, root_path, output_path=None, config_path=None):
        self.root_path = Path(root_path).resolve()
        self.output_path = Path(output_path) if output_path else self.root_path
        self.template_path = Path(__file__).parent / "directory-template.html"
        self.config = self._load_config(config_path)

    def _load_config(self, config_path):
        if config_path:
            cp = Path(config_path)
        else:
            cp = Path(__file__).parent / "config.json"
        try:
            with open(cp, 'r', encoding='utf-8') as f:
                return json.load(f)
        except (FileNotFoundError, json.JSONDecodeError):
            return {}

    def _cfg(self, *keys, default=None):
        val = self.config
        for k in keys:
            if isinstance(val, dict) and k in val:
                val = val[k]
            else:
                return default
        return val

    def get_file_size(self, file_path):
        try:
            size = file_path.stat().st_size
            for unit in ['B', 'KB', 'MB', 'GB', 'TB']:
                if size < 1024.0:
                    return f"{size:.1f} {unit}" if size >= 10 else f"{size:.0f} {unit}"
                size /= 1024.0
            return f"{size:.1f} PB"
        except (OSError, PermissionError):
            return "-"

    def get_file_size_bytes(self, file_path):
        try:
            return file_path.stat().st_size
        except (OSError, PermissionError):
            return 0

    def get_file_date(self, file_path):
        try:
            timestamp = file_path.stat().st_mtime
            dt = datetime.datetime.fromtimestamp(timestamp)
            return dt.strftime("%Y-%b-%d %H:%M")
        except (OSError, PermissionError):
            return "-"

    def get_file_date_sort(self, file_path):
        try:
            timestamp = file_path.stat().st_mtime
            dt = datetime.datetime.fromtimestamp(timestamp)
            return dt.strftime("%Y-%m-%d %H:%M:%S")
        except (OSError, PermissionError):
            return "0000-00-00 00:00:00"

    def is_hidden_file(self, file_path):
        return (
            file_path.name.startswith('.')
            or file_path.name.startswith('~')
            or file_path.name.startswith('_')
        )

    def should_include_file(self, file_path):
        if self.is_hidden_file(file_path):
            return False
        exclude_files = self._cfg('exclude', 'files', default=[])
        exclude_exts = self._cfg('exclude', 'extensions', default=[])
        name_lower = file_path.name.lower()
        for pat in exclude_files:
            if name_lower == pat.lower():
                return False
        for pat in exclude_exts:
            if pat.startswith('.') and name_lower.endswith(pat.lower()):
                return False
        return True

    def _get_file_icon_info(self, file_path):
        if file_path.is_dir():
            return 'folder', 'icon-folder', FILE_ICON_EMOJIS.get('folder', '📁')

        name = file_path.name
        name_lower = name.lower()
        icon_map = self._cfg('file_icons', default={})

        if name == 'Makefile' or name.startswith('Makefile.'):
            icon_type = 'build'
            return icon_type, f'icon-{icon_type}', FILE_ICON_EMOJIS.get(icon_type, '📄')

        for ext_key, ext_info in icon_map.items():
            if ext_key == 'default' or ext_key == 'folder':
                continue
            if name_lower.endswith(ext_key.lower()):
                icon_type = ext_info.get('icon', 'file')
                return icon_type, f'icon-{icon_type}', FILE_ICON_EMOJIS.get(icon_type, '📄')

        return 'file', 'icon-default', FILE_ICON_EMOJIS.get('file', '📄')

    def _get_ext_badge(self, file_path):
        if file_path.is_dir():
            return ''
        name = file_path.name
        name_lower = name.lower()
        if name == 'Makefile' or name.startswith('Makefile.'):
            return '<span class="file-ext-badge" style="background:#ffedd5;color:#d97706">mk</span>'

        suffixes = file_path.suffixes
        if not suffixes:
            return ''

        ext = suffixes[-1].lstrip('.').lower()
        if not ext:
            return ''

        color = FILE_EXT_BADGE_COLORS.get(ext, '#6b7280')
        return f'<span class="file-ext-badge" style="background:{color}15;color:{color}">{html_module.escape(ext)}</span>'

    def generate_file_row(self, file_path, is_parent=False):
        if is_parent:
            return (
                '<tr class="parent-row" data-type="parent" data-name="..">'
                '<td><div class="fname"><span class="fname-icon icon-parent">⬆️</span>'
                '<a href="../" class="fname-link parent-link">上级目录 /</a></div></td>'
                '<td class="cell-size">-</td>'
                '<td class="cell-date">-</td>'
                '<td></td>'
                '</tr>'
            )

        name = file_path.name
        is_dir = file_path.is_dir()
        icon_type, icon_class, icon_emoji = self._get_file_icon_info(file_path)
        ext_badge = self._get_ext_badge(file_path)

        if is_dir:
            display_name = f"{name}/"
            href = f"{name}/"
            link_class = "dir-link"
            size_display = "-"
            size_bytes = "0"
            dl_btn = ""
        else:
            display_name = name
            href = name
            link_class = ""
            size_display = self.get_file_size(file_path)
            size_bytes = str(self.get_file_size_bytes(file_path))
            dl_btn = f'<a href="{html_module.escape(href)}" class="dl-btn" download><span class="dl-icon">⬇</span>下载</a>'

        date_display = self.get_file_date(file_path)
        date_sort = self.get_file_date_sort(file_path)
        row_type = "dir" if is_dir else "file"

        return (
            f'<tr data-type="{row_type}" data-name="{html_module.escape(name)}" '
            f'data-size="{size_bytes}" data-date="{date_sort}">'
            f'<td><div class="fname">'
            f'<span class="fname-icon {icon_class}">{icon_emoji}</span>'
            f'<a href="{html_module.escape(href)}" class="fname-link {link_class}">{html_module.escape(display_name)}</a>'
            f'{ext_badge}'
            f'</div></td>'
            f'<td class="cell-size">{size_display}</td>'
            f'<td class="cell-date">{date_display}</td>'
            f'<td>{dl_btn}</td>'
            f'</tr>'
        )

    def load_template(self):
        try:
            with open(self.template_path, 'r', encoding='utf-8') as f:
                return f.read()
        except FileNotFoundError:
            print(f"错误：找不到模板文件 {self.template_path}")
            sys.exit(1)

    def _build_nav_links(self):
        links = self._cfg('header', 'nav_links', default=[])
        icon_map = {'home': '🏠', 'chip': '🔧', 'video': '🎬'}
        parts = []
        for link in links:
            icon = icon_map.get(link.get('icon', ''), '')
            text = html_module.escape(link.get('text', ''))
            href = html_module.escape(link.get('href', '/'))
            parts.append(f'<a href="{href}"><span class="nav-icon">{icon}</span>{text}</a>')
        return '\n                '.join(parts)

    def _build_breadcrumb(self, directory_path):
        relative_to_root = directory_path.relative_to(self.root_path)
        path_parts = list(relative_to_root.parts)

        if not path_parts:
            return ''

        start_from = self._cfg('breadcrumb', 'start_from', default='')
        fallback_levels = self._cfg('breadcrumb', 'fallback_levels', default=2)

        if start_from and start_from in path_parts:
            start_index = path_parts.index(start_from)
        else:
            start_index = max(0, len(path_parts) - fallback_levels)

        display_parts = path_parts[start_index:]
        bc_parts = []

        for i, part in enumerate(display_parts):
            sep = '<span class="bc-sep">/</span>'
            if i == len(display_parts) - 1:
                bc_parts.append(f'{sep} <span class="bc-current">{html_module.escape(part)}</span>')
            else:
                levels_up = len(display_parts) - i - 1
                if levels_up > 0:
                    relative_path = '../' * levels_up
                else:
                    relative_path = './'
                target_path = '/'.join(display_parts[i + 1:])
                if target_path:
                    relative_path += target_path + '/'
                bc_parts.append(f'{sep} <a href="{relative_path}" class="bc-part">{html_module.escape(part)}</a>')

        return ' '.join(bc_parts)

    def _build_intro_features(self):
        features = self._cfg('intro', 'features', default=[])
        parts = []
        for feat in features:
            icon = feat.get('icon', '✨')
            text = html_module.escape(feat.get('text', ''))
            parts.append(f'<div class="feat"><span class="feat-icon">{icon}</span>{text}</div>')
        return '\n                    '.join(parts)

    def _build_footer_links(self):
        links = self._cfg('footer', 'links', default=[])
        parts = []
        for link in links:
            text = html_module.escape(link.get('text', ''))
            href = html_module.escape(link.get('href', '/'))
            parts.append(f'<a href="{href}">{text}</a>')
        return '\n                '.join(parts)

    def _markdown_to_html(self, text):
        lines = text.split('\n')
        html_parts = []
        in_code_block = False
        in_list = False
        list_type = None
        in_table = False
        table_rows = []

        def close_list():
            nonlocal in_list, list_type
            if in_list:
                html_parts.append(f'</{list_type}>')
                in_list = False

        def close_table():
            nonlocal in_table, table_rows
            if in_table and table_rows:
                html_parts.append('<table>')
                for i, cells in enumerate(table_rows):
                    tag = 'th' if i == 0 else 'td'
                    row = ''.join(f'<{tag}>{self._md_inline(c.strip())}</{tag}>' for c in cells)
                    html_parts.append(f'<tr>{row}</tr>')
                html_parts.append('</table>')
                table_rows = []
                in_table = False

        for line in lines:
            stripped = line.strip()

            if stripped.startswith('```'):
                close_list()
                close_table()
                if in_code_block:
                    html_parts.append('</code></pre>')
                    in_code_block = False
                else:
                    html_parts.append('<pre><code>')
                    in_code_block = True
                continue

            if in_code_block:
                html_parts.append(html_module.escape(line))
                continue

            if not stripped:
                close_list()
                close_table()
                html_parts.append('')
                continue

            if stripped.startswith('#'):
                close_list()
                close_table()
                m = re.match(r'^(#{1,6})\s+(.+)$', stripped)
                if m:
                    level = len(m.group(1))
                    content = self._md_inline(m.group(2))
                    html_parts.append(f'<h{level}>{content}</h{level}>')
                continue

            if stripped.startswith('> '):
                close_list()
                close_table()
                content = self._md_inline(stripped[2:])
                html_parts.append(f'<blockquote><p>{content}</p></blockquote>')
                continue

            if stripped.startswith('---') or stripped.startswith('***'):
                close_list()
                close_table()
                html_parts.append('<hr>')
                continue

            if '|' in stripped and re.match(r'^\|.*\|$', stripped):
                close_list()
                cells = [c for c in stripped.split('|')[1:-1]]
                if all(re.match(r'^[\s:-]+$', c) for c in cells):
                    continue
                if not in_table:
                    in_table = True
                    table_rows = []
                table_rows.append(cells)
                continue

            m_ul = re.match(r'^[-*+]\s+(.+)$', stripped)
            m_ol = re.match(r'^\d+\.\s+(.+)$', stripped)

            if m_ul:
                close_table()
                if not in_list or list_type != 'ul':
                    close_list()
                    html_parts.append('<ul>')
                    in_list = True
                    list_type = 'ul'
                html_parts.append(f'<li>{self._md_inline(m_ul.group(1))}</li>')
                continue

            if m_ol:
                close_table()
                if not in_list or list_type != 'ol':
                    close_list()
                    html_parts.append('<ol>')
                    in_list = True
                    list_type = 'ol'
                html_parts.append(f'<li>{self._md_inline(m_ol.group(1))}</li>')
                continue

            close_list()
            close_table()
            html_parts.append(f'<p>{self._md_inline(stripped)}</p>')

        if in_code_block:
            html_parts.append('</code></pre>')
        close_list()
        close_table()

        return '\n'.join(html_parts)

    def _md_inline(self, text):
        text = html_module.escape(text)
        text = re.sub(r'\*\*(.+?)\*\*', r'<strong>\1</strong>', text)
        text = re.sub(r'\*(.+?)\*', r'<em>\1</em>', text)
        text = re.sub(r'`([^`]+)`', r'<code>\1</code>', text)
        text = re.sub(r'\[([^\]]+)\]\(([^)]+)\)', r'<a href="\2">\1</a>', text)
        return text

    def _load_readme(self, directory_path):
        readme_names = ['README.md', 'readme.md', 'Readme.md', 'README', 'readme']
        for name in readme_names:
            readme_path = directory_path / name
            if readme_path.exists() and readme_path.is_file():
                try:
                    with open(readme_path, 'r', encoding='utf-8') as f:
                        content = f.read()
                    if content.strip():
                        html_body = self._markdown_to_html(content)
                        return (
                            '<section class="readme-section">'
                            '<div class="container">'
                            '<div class="readme-card">'
                            '<div class="readme-header">'
                            '<span class="readme-icon">📖</span>'
                            '<span>README.md</span>'
                            '</div>'
                            '<div class="readme-body">'
                            + html_body +
                            '</div>'
                            '</div>'
                            '</div>'
                            '</section>'
                        )
                except (OSError, UnicodeDecodeError):
                    pass
        return ''

    def generate_directory_html(self, directory_path):
        directory_path = Path(directory_path)

        if not directory_path.exists() or not directory_path.is_dir():
            print(f"错误：目录不存在或不是目录 {directory_path}")
            return False

        try:
            relative_path = directory_path.relative_to(self.root_path)
            if relative_path == Path('.'):
                display_path = "/"
                dir_display = "/"
            else:
                display_path = str(relative_path).replace('\\', '/')
                dir_display = "/" + display_path + "/"
        except ValueError:
            display_path = str(directory_path).replace('\\', '/')
            dir_display = display_path

        items = []
        if directory_path != self.root_path:
            items.append(("..", None, True))

        try:
            for item in sorted(directory_path.iterdir()):
                if self.should_include_file(item):
                    items.append((item.name, item, False))
        except PermissionError:
            print(f"警告：没有权限访问目录 {directory_path}")
            return False

        file_rows = []
        dir_count = 0
        file_count = 0
        for name, item_path, is_parent in items:
            if is_parent:
                file_rows.append(self.generate_file_row(None, is_parent=True))
            else:
                file_rows.append(self.generate_file_row(item_path))
                if item_path.is_dir():
                    dir_count += 1
                else:
                    file_count += 1

        breadcrumb_html = self._build_breadcrumb(directory_path)
        readme_html = self._load_readme(directory_path)

        site_title = self._cfg('site', 'title', default='下载中心')
        page_title = f"{display_path} - {site_title}" if display_path != "/" else site_title
        language = self._cfg('site', 'language', default='zh-CN')
        nav_links = self._build_nav_links()
        intro_title = self._cfg('intro', 'title', default='✨ 资源下载中心')
        intro_content = self._cfg('intro', 'content', default='')
        intro_features = self._build_intro_features()
        intro_enabled = self._cfg('intro', 'enabled', default=True)
        footer_copyright = self._cfg('footer', 'copyright', default='')
        footer_links = self._build_footer_links()
        stats_info = f"共 {dir_count} 个文件夹, {file_count} 个文件"

        template = self.load_template()

        replacements = {
            '{{language}}': language,
            '{{page_title}}': html_module.escape(page_title),
            '{{site_title}}': html_module.escape(site_title),
            '{{nav_links}}': nav_links,
            '{{breadcrumb_parts}}': breadcrumb_html,
            '{{readme_section}}': readme_html,
            '{{intro_title}}': intro_title,
            '{{intro_content}}': html_module.escape(intro_content),
            '{{intro_features}}': intro_features,
            '{{dir_display}}': html_module.escape(dir_display),
            '{{file_list}}': '\n'.join(file_rows),
            '{{stats_info}}': stats_info,
            '{{footer_copyright}}': html_module.escape(footer_copyright),
            '{{footer_links}}': footer_links,
        }

        html_content = template
        for key, val in replacements.items():
            html_content = html_content.replace(key, val)

        if not intro_enabled:
            html_content = html_content.replace(
                '<section class="intro-section">',
                '<section class="intro-section" style="display:none">'
            )

        output_file = directory_path / "index.html"
        try:
            with open(output_file, 'w', encoding='utf-8') as f:
                f.write(html_content)
            print(f"已生成: {output_file}")
            return True
        except Exception as e:
            print(f"错误：写入文件失败 {output_file}: {e}")
            return False

    def generate_all_directories(self, start_path=None):
        if start_path is None:
            start_path = self.root_path

        start_path = Path(start_path)
        if not start_path.exists():
            print(f"错误：起始目录不存在 {start_path}")
            return False

        success_count = 0
        total_count = 0

        for directory in start_path.rglob('*'):
            if directory.is_dir():
                total_count += 1
                if self.generate_directory_html(directory):
                    success_count += 1

        print(f"\n生成完成！成功: {success_count}/{total_count}")
        return success_count > 0

    def generate_root_index(self):
        return self.generate_directory_html(self.root_path)


def main():
    import argparse

    parser = argparse.ArgumentParser(description='生成官方镜像文件下载站点页面')
    parser.add_argument('path', nargs='?', default='.', help='要处理的目录路径（默认：当前目录）')
    parser.add_argument('-o', '--output', help='输出目录路径（默认：与源目录相同）')
    parser.add_argument('-r', '--recursive', action='store_true', help='递归处理所有子目录')
    parser.add_argument('-c', '--config', help='配置文件路径（默认：同目录下config.json）')
    parser.add_argument('--dry-run', action='store_true', help='试运行模式')

    args = parser.parse_args()
    root_path = Path(args.path).resolve()

    if not root_path.exists():
        print(f"错误：目录不存在 {root_path}")
        sys.exit(1)

    generator = DirectoryGenerator(root_path, args.output, args.config)

    if args.dry_run:
        print(f"试运行模式 - 根目录: {root_path}")
        print(f"输出目录: {generator.output_path}")
        if args.recursive:
            dirs = [d for d in root_path.rglob('*') if d.is_dir()]
            print(f"将要处理 {len(dirs)} 个目录")
        else:
            print(f"将要处理目录: {root_path}")
        return

    if args.recursive:
        generator.generate_all_directories()
    else:
        generator.generate_directory_html(root_path)


if __name__ == '__main__':
    main()
