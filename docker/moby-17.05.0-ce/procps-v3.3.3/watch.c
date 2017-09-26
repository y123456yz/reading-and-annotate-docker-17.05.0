/*
 * watch -- execute a program repeatedly, displaying output fullscreen
 *
 * Based on the original 1991 'watch' by Tony Rems <rembo@unisoft.com>
 * (with mods and corrections by Francois Pinard).
 *
 * Substantially reworked, new features (differences option, SIGWINCH
 * handling, unlimited command length, long line handling) added Apr
 * 1999 by Mike Coleman <mkc@acm.org>.
 *
 * Changes by Albert Cahalan, 2002-2003.
 * stderr handling, exec, and beep option added by Morty Abzug, 2008
 * Unicode Support added by Jarrod Lowe <procps@rrod.net> in 2009.
 *
 * This library is free software; you can redistribute it and/or
 * modify it under the terms of the GNU Lesser General Public
 * License as published by the Free Software Foundation; either
 * version 2.1 of the License, or (at your option) any later version.
 *
 * This library is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 * Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public
 * License along with this library; if not, write to the Free Software
 * Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301  USA
 */

#include "c.h"
#include "config.h"
#include "fileutils.h"
#include "nls.h"
#include "proc/procps.h"
#include "strutils.h"
#include "xalloc.h"
#include <ctype.h>
#include <errno.h>
#include <errno.h>
#include <getopt.h>
#include <locale.h>
#include <locale.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/time.h>
#include <sys/wait.h>
#include <termios.h>
#include <termios.h>
#include <time.h>
#include <unistd.h>
#ifdef WITH_WATCH8BIT
# include <wchar.h>
# include <ncursesw/ncurses.h>
#else
# include <ncurses.h>
#endif	/* WITH_WATCH8BIT */

#ifdef FORCE_8BIT
# undef isprint
# define isprint(x) ( (x>=' '&&x<='~') || (x>=0xa0) )
#endif

/* Boolean command line options */
static int flags;
#define WATCH_DIFF	(1 << 1)
#define WATCH_CUMUL	(1 << 2)
#define WATCH_EXEC	(1 << 3)
#define WATCH_BEEP	(1 << 4)
#define WATCH_COLOR	(1 << 5)
#define WATCH_ERREXIT	(1 << 6)
#define WATCH_CHGEXIT	(1 << 7)

static int curses_started = 0;
static long height = 24, width = 80;
static int screen_size_changed = 0;
static int first_screen = 1;
static int show_title = 2;	/* number of lines used, 2 or 0 */
static int precise_timekeeping = 0;

#define min(x,y) ((x) > (y) ? (y) : (x))
#define MAX_ANSIBUF 10

static void __attribute__ ((__noreturn__))
    usage(FILE * out)
{
	fputs(USAGE_HEADER, out);
	fprintf(out,
              _(" %s [options] command\n"), program_invocation_short_name);
	fputs(USAGE_OPTIONS, out);
	fputs(_("  -b, --beep             beep if command has a non-zero exit\n"
		"  -c, --color            interpret ANSI color sequences\n"
		"  -d, --differences[=<permanent>]\n"
                "                         highlight changes between updates\n"
		"  -e, --errexit          exit if command has a non-zero exit\n"
		"  -g, --chgexit          exit when output from command changes\n"
		"  -n, --interval <secs>  seconds to wait between updates\n"
		"  -p, --precise          attempt run command in precise intervals\n"
		"  -t, --no-title         turn off header\n"
		"  -x, --exec             pass command to exec instead of \"sh -c\"\n"), out);
	fputs(USAGE_SEPARATOR, out);
	fputs(USAGE_HELP, out);
	fputs(_(" -v, --version  output version information and exit\n"), out);
	fprintf(out, USAGE_MAN_TAIL("watch(1)"));

	exit(out == stderr ? EXIT_FAILURE : EXIT_SUCCESS);
}

static void init_ansi_colors(void)
{
	int i;
	short ncurses_colors[] = {
		COLOR_BLACK, COLOR_RED, COLOR_GREEN, COLOR_YELLOW, COLOR_BLUE,
		COLOR_MAGENTA, COLOR_CYAN, COLOR_WHITE
	};

	for (i = 0; i < 8; i++)
		init_pair(i + 1, ncurses_colors[i], -1);
}

static void set_ansi_attribute(const int attrib)
{
	switch (attrib) {
	case -1:
		return;
	case 0:
		standend();
		return;
	case 1:
		attrset(A_BOLD);
		return;
	}
	if (attrib >= 30 && attrib <= 37) {
		color_set(attrib - 29, NULL);
		return;
	}
}

static void process_ansi(FILE * fp)
{
	int i, c, num1, num2;
	char buf[MAX_ANSIBUF];
	char *nextnum;

	c = getc(fp);
	if (c != '[') {
		ungetc(c, fp);
		return;
	}
	for (i = 0; i < MAX_ANSIBUF; i++) {
		c = getc(fp);
		/* COLOUR SEQUENCE ENDS in 'm' */
		if (c == 'm') {
			buf[i] = '\0';
			break;
		}
		if (c < '0' && c > '9' && c != ';') {
			while (--i >= 0)
				ungetc(buf[i], fp);
			return;
		}
		buf[i] = (char)c;
	}
	num1 = strtol(buf, &nextnum, 10);
	if (nextnum != buf && nextnum[0] != '\0')
		num2 = strtol(nextnum + 1, NULL, 10);
	else
		num2 = -1;
	set_ansi_attribute(num1);
	set_ansi_attribute(num2);
}

static void __attribute__ ((__noreturn__)) do_exit(int status)
{
	if (curses_started)
		endwin();
	exit(status);
}

/* signal handler */
static void die(int notused __attribute__ ((__unused__)))
{
	do_exit(EXIT_SUCCESS);
}

static void winch_handler(int notused __attribute__ ((__unused__)))
{
	screen_size_changed = 1;
}

static char env_col_buf[24];
static char env_row_buf[24];
static int incoming_cols;
static int incoming_rows;

static void get_terminal_size(void)
{
	struct winsize w;
	if (!incoming_cols) {
		/* have we checked COLUMNS? */
		const char *s = getenv("COLUMNS");
		incoming_cols = -1;
		if (s && *s) {
			long t;
			char *endptr;
			t = strtol(s, &endptr, 0);
			if (!*endptr && 0 < t)
				incoming_cols = t;
			width = incoming_cols;
			snprintf(env_col_buf, sizeof env_col_buf, "COLUMNS=%ld",
				 width);
			putenv(env_col_buf);
		}
	}
	if (!incoming_rows) {
		/* have we checked LINES? */
		const char *s = getenv("LINES");
		incoming_rows = -1;
		if (s && *s) {
			long t;
			char *endptr;
			t = strtol(s, &endptr, 0);
			if (!*endptr && 0 < t)
				incoming_rows = t;
			height = incoming_rows;
			snprintf(env_row_buf, sizeof env_row_buf, "LINES=%ld",
				 height);
			putenv(env_row_buf);
		}
	}
	if (ioctl(STDERR_FILENO, TIOCGWINSZ, &w) == 0) {
		if (incoming_cols < 0 || incoming_rows < 0) {
			if (incoming_rows < 0 && w.ws_row > 0) {
				height = w.ws_row;
				snprintf(env_row_buf, sizeof env_row_buf,
					 "LINES=%ld", height);
				putenv(env_row_buf);
			}
			if (incoming_cols < 0 && w.ws_col > 0) {
				width = w.ws_col;
				snprintf(env_col_buf, sizeof env_col_buf,
					 "COLUMNS=%ld", width);
				putenv(env_col_buf);
			}
		}
	}
}

/* get current time in usec */
typedef unsigned long long watch_usec_t;
#define USECS_PER_SEC (1000000ull)
watch_usec_t get_time_usec()
{
	struct timeval now;
	gettimeofday(&now, NULL);
	return USECS_PER_SEC * now.tv_sec + now.tv_usec;
}

#ifdef WITH_WATCH8BIT
/* read a wide character from a popen'd stream */
#define MAX_ENC_BYTES 16
wint_t my_getwc(FILE * s);
wint_t my_getwc(FILE * s)
{
	/* assuming no encoding ever consumes more than 16 bytes */
	char i[MAX_ENC_BYTES];
	int byte = 0;
	int convert;
	int x;
	wchar_t rval;
	while (1) {
		i[byte] = getc(s);
		if (i[byte] == EOF) {
			return WEOF;
		}
		byte++;
		errno = 0;
		mbtowc(NULL, NULL, 0);
		convert = mbtowc(&rval, i, byte);
		x = errno;
		if (convert > 0) {
			/* legal conversion */
			return rval;
		}
		if (byte == MAX_ENC_BYTES) {
			while (byte > 1) {
				/* at least *try* to fix up */
				ungetc(i[--byte], s);
			}
			errno = -EILSEQ;
			return WEOF;
		}
	}
}
#endif	/* WITH_WATCH8BIT */

void output_header(char *restrict command, double interval)
{
	time_t t = time(NULL);
	char *ts = ctime(&t);
	int tsl = strlen(ts);
	char *header;

	/*
	 * left justify interval and command, right justify time,
	 * clipping all to fit window width
	 */
	int hlen = asprintf(&header, _("Every %.1fs: "), interval);

	/*
	 * the rules:
	 *   width < tsl : print nothing
	 *   width < tsl + hlen + 1: print ts
	 *   width = tsl + hlen + 1: print header, ts
	 *   width < tsl + hlen + 4: print header, ..., ts
	 *   width < tsl + hlen +    wcommand_columns: print header,
	 *                           truncated wcommand, ..., ts
	 *   width > "": print header, wcomand, ts
	 * this is slightly different from how it used to be
	 */
	if (width < tsl) {
		free(header);
		return;
	}
	if (tsl + hlen + 1 <= width) {
		mvaddstr(0, 0, header);
		if (tsl + hlen + 2 <= width) {
			if (width < tsl + hlen + 4) {
				mvaddstr(0, width - tsl - 4, "... ");
			} else {
#ifdef WITH_WATCH8BIT
				if (width < tsl + hlen + wcommand_columns) {
					/* print truncated */
					int available = width - tsl - hlen;
					int in_use = wcommand_columns;
					int wcomm_len = wcommand_characters;
					while (available - 4 < in_use) {
						wcomm_len--;
						in_use = wcswidth(wcommand, wcomm_len);
					}
					mvaddnwstr(0, hlen, wcommand, wcomm_len);
					mvaddstr(0, width - tsl - 4, "... ");
				} else {
					mvaddwstr(0, hlen, wcommand);
				}
#else
				mvaddnstr(0, hlen, command, width - tsl - hlen);
#endif	/* WITH_WATCH8BIT */
			}
		}
	}
	mvaddstr(0, width - tsl + 1, ts);
	free(header);
	return;
}

int run_command(char *restrict command, char **restrict command_argv)
{
	FILE *p;
	int x, y;
	int oldeolseen = 1;
	int pipefd[2];
	pid_t child;
	int exit_early = 0;
	int status;

	/* allocate pipes */
	if (pipe(pipefd) < 0)
		xerr(7, _("unable to create IPC pipes"));

	/* flush stdout and stderr, since we're about to do fd stuff */
	fflush(stdout);
	fflush(stderr);

	/* fork to prepare to run command */
	child = fork();

	if (child < 0) {		/* fork error */
		xerr(2, _("unable to fork process"));
	} else if (child == 0) {	/* in child */
		close(pipefd[0]);		/* child doesn't need read side of pipe */
		close(1);			/* prepare to replace stdout with pipe */
		if (dup2(pipefd[1], 1) < 0) {	/* replace stdout with write side of pipe */
			xerr(3, _("dup2 failed"));
		}
		dup2(1, 2);			/* stderr should default to stdout */

		if (flags & WATCH_EXEC) {	/* pass command to exec instead of system */
			if (execvp(command_argv[0], command_argv) == -1) {
				xerr(4, _("unable to execute '%s'"),
				     command_argv[0]);
			}
		} else {
			status = system(command);	/* watch manpage promises sh quoting */
			/* propagate command exit status as child exit status */
			if (!WIFEXITED(status)) {	/* child exits nonzero if command does */
				exit(EXIT_FAILURE);
			} else {
				exit(WEXITSTATUS(status));
			}
		}
	}

	/* otherwise, we're in parent */
	close(pipefd[1]);	/* close write side of pipe */
	if ((p = fdopen(pipefd[0], "r")) == NULL)
		xerr(5, _("fdopen"));

	for (y = show_title; y < height; y++) {
		int eolseen = 0, tabpending = 0;
#ifdef WITH_WATCH8BIT
		wint_t carry = WEOF;
#endif
		for (x = 0; x < width; x++) {
#ifdef WITH_WATCH8BIT
			wint_t c = ' ';
#else
			int c = ' ';
#endif
			int attr = 0;

			if (!eolseen) {
				/* if there is a tab pending, just
				 * spit spaces until the next stop
				 * instead of reading characters */
				if (!tabpending)
#ifdef WITH_WATCH8BIT
					do {
						if (carry == WEOF) {
							c = my_getwc(p);
						} else {
							c = carry;
							carry = WEOF;
						}
					} while (c != WEOF && !isprint(c)
						 && c < 12
						 && wcwidth(c) == 0
						 && c != L'\n'
						 && c != L'\t'
						 && (c != L'\033'
						     || !(flags & WATCH_COLOR)));
#else
					do
						c = getc(p);
					while (c != EOF && !isprint(c)
					       && c != '\n'
					       && c != '\t'
					       && (c != L'\033'
						   || !(flags & WATCH_COLOR)));
#endif
				if (c == L'\033' && (flags & WATCH_COLOR)) {
					x--;
					process_ansi(p);
					continue;
				}
				if (c == L'\n')
					if (!oldeolseen && x == 0) {
						x = -1;
						continue;
					} else
						eolseen = 1;
				else if (c == L'\t')
					tabpending = 1;
#ifdef WITH_WATCH8BIT
				if (x == width - 1 && wcwidth(c) == 2) {
					y++;
					x = -1;		/* process this double-width */
					carry = c;	/* character on the next line */
					continue;	/* because it won't fit here */
				}
				if (c == WEOF || c == L'\n' || c == L'\t')
					c = L' ';
#else
				if (c == EOF || c == '\n' || c == '\t')
					c = ' ';
#endif
				if (tabpending && (((x + 1) % 8) == 0))
					tabpending = 0;
			}
			move(y, x);
			if (!first_screen && !exit_early && (flags & WATCH_CHGEXIT)) {
#ifdef WITH_WATCH8BIT
				cchar_t oldc;
				in_wch(&oldc);
				exit_early = (wchar_t) c != oldc.chars[0];
#else
				chtype oldch = inch();
				unsigned char oldc = oldch & A_CHARTEXT;
				exit_early = (unsigned char)c != oldc;
#endif
			}
			if (flags & WATCH_DIFF) {
#ifdef WITH_WATCH8BIT
				cchar_t oldc;
				in_wch(&oldc);
				attr = !first_screen
				    && ((wchar_t) c != oldc.chars[0]
					||
					((flags & WATCH_CUMUL)
					 && (oldc.attr & A_ATTRIBUTES)));
#else
				chtype oldch = inch();
				unsigned char oldc = oldch & A_CHARTEXT;
				attr = !first_screen
				    && ((unsigned char)c != oldc
					||
					((flags & WATCH_CUMUL)
					 && (oldch & A_ATTRIBUTES)));
#endif
			}
			if (attr)
				standout();
#ifdef WITH_WATCH8BIT
			addnwstr((wchar_t *) & c, 1);
#else
			addch(c);
#endif
			if (attr)
				standend();
#ifdef WITH_WATCH8BIT
			if (wcwidth(c) == 0) {
				x--;
			}
			if (wcwidth(c) == 2) {
				x++;
			}
#endif
		}
		oldeolseen = eolseen;
	}

	fclose(p);

	/* harvest child process and get status, propagated from command */
	if (waitpid(child, &status, 0) < 0)
		xerr(8, _("waitpid"));

	/* if child process exited in error, beep if option_beep is set */
	if ((!WIFEXITED(status) || WEXITSTATUS(status))) {
		if (flags & WATCH_BEEP)
			beep();
		if (flags & WATCH_ERREXIT) {
			mvaddstr(height - 1, 0,
				 _("command exit with a non-zero status, press a key to exit"));
			refresh();
			fgetc(stdin);
			endwin();
			exit(8);
		}
	}
	first_screen = 0;
	refresh();
	return exit_early;
}

int main(int argc, char *argv[])
{
	int optc;
	double interval = 2;
	char *command;
	char **command_argv;
	int command_length = 0;	/* not including final \0 */
	watch_usec_t next_loop;	/* next loop time in us, used for precise time
				 * keeping only */
#ifdef WITH_WATCH8BIT
	wchar_t *wcommand = NULL;
	int wcommand_columns = 0;	/* not including final \0 */
	int wcommand_characters = 0;	/* not including final \0 */
#endif	/* WITH_WATCH8BIT */

	static struct option longopts[] = {
		{"color", no_argument, 0, 'c'},
		{"differences", optional_argument, 0, 'd'},
		{"help", no_argument, 0, 'h'},
		{"interval", required_argument, 0, 'n'},
		{"beep", no_argument, 0, 'b'},
		{"errexit", no_argument, 0, 'e'},
		{"chgexit", no_argument, 0, 'g'},
		{"exec", no_argument, 0, 'x'},
		{"precise", no_argument, 0, 'p'},
		{"no-title", no_argument, 0, 't'},
		{"version", no_argument, 0, 'v'},
		{0, 0, 0, 0}
	};

	program_invocation_name = program_invocation_short_name;
	setlocale(LC_ALL, "");
	bindtextdomain(PACKAGE, LOCALEDIR);
	textdomain(PACKAGE);
	atexit(close_stdout);

	while ((optc =
		getopt_long(argc, argv, "+bced::ghn:pvtx", longopts, (int *)0))
	       != EOF) {
		switch (optc) {
		case 'b':
			flags |= WATCH_BEEP;
			break;
		case 'c':
			flags |= WATCH_COLOR;
			break;
		case 'd':
			flags |= WATCH_DIFF;
			if (optarg)
				flags |= WATCH_CUMUL;
			break;
		case 'e':
			flags |= WATCH_ERREXIT;
			break;
		case 'g':
			flags |= WATCH_CHGEXIT;
			break;
		case 't':
			show_title = 0;
			break;
		case 'x':
			flags |= WATCH_EXEC;
			break;
		case 'n':
			interval = strtod_or_err(optarg, _("failed to parse argument"));
			if (interval < 0.1)
				interval = 0.1;
			if (interval > ~0u / 1000000)
				interval = ~0u / 1000000;
			break;
		case 'p':
			precise_timekeeping = 1;
			break;
		case 'h':
			usage(stdout);
			break;
		case 'v':
			printf(PROCPS_NG_VERSION);
			return EXIT_SUCCESS;
		default:
			usage(stderr);
			break;
		}
	}

	if (optind >= argc)
		usage(stderr);

	/* save for later */
	command_argv = &(argv[optind]);

	command = xstrdup(argv[optind++]);
	command_length = strlen(command);
	for (; optind < argc; optind++) {
		char *endp;
		int s = strlen(argv[optind]);
		/* space and \0 */
		command = xrealloc(command, command_length + s + 2);
		endp = command + command_length;
		*endp = ' ';
		memcpy(endp + 1, argv[optind], s);
		/* space then string length */
		command_length += 1 + s;
		command[command_length] = '\0';
	}

#ifdef WITH_WATCH8BIT
	/* convert to wide for printing purposes */
	/*mbstowcs(NULL, NULL, 0); */
	wcommand_characters = mbstowcs(NULL, command, 0);
	if (wcommand_characters < 0) {
		fprintf(stderr, _("unicode handling error\n"));
		exit(EXIT_FAILURE);
	}
	wcommand =
	    (wchar_t *) malloc((wcommand_characters + 1) * sizeof(wcommand));
	if (wcommand == NULL) {
		fprintf(stderr, _("unicode handling error (malloc)\n"));
		exit(EXIT_FAILURE);
	}
	mbstowcs(wcommand, command, wcommand_characters + 1);
	wcommand_columns = wcswidth(wcommand, -1);
#endif	/* WITH_WATCH8BIT */

	get_terminal_size();

	/* Catch keyboard interrupts so we can put tty back in a sane
	 * state.  */
	signal(SIGINT, die);
	signal(SIGTERM, die);
	signal(SIGHUP, die);
	signal(SIGWINCH, winch_handler);

	/* Set up tty for curses use.  */
	curses_started = 1;
	initscr();
	if (flags & WATCH_COLOR) {
		if (has_colors()) {
			start_color();
			use_default_colors();
			init_ansi_colors();
		} else {
			flags |= WATCH_COLOR;
		}
	}
	nonl();
	noecho();
	cbreak();

	if (precise_timekeeping)
		next_loop = get_time_usec();

	while (1) {
		if (screen_size_changed) {
			get_terminal_size();
			resizeterm(height, width);
			clear();
			/* redrawwin(stdscr); */
			screen_size_changed = 0;
			first_screen = 1;
		}

		if (show_title)
			output_header(command, interval);

		if (run_command(command, command_argv))
			break;
		if (precise_timekeeping) {
			watch_usec_t cur_time = get_time_usec();
			next_loop += USECS_PER_SEC * interval;
			if (cur_time < next_loop)
				usleep(next_loop - cur_time);
		} else
			usleep(interval * 1000000);
	}

	endwin();
	return EXIT_SUCCESS;
}